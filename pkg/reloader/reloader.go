package reloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/apex/log"
	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
)

// Reloader can watch config files and trigger reloads of a Prometheus server.
// It optionally substitutes environment variables in the configuration.
// Referenced environment variables must be of the form `$(var)` (not `$var` or `${var}`).
type Reloader struct {
	reloadURL     *url.URL
	cfgFile       string
	watchInterval time.Duration
	retryInterval time.Duration

	lastCfgHash []byte
}

// New creates a new reloader that watches the given config file or directory
// and does HTTP POST.
func New(reloadURL *url.URL, cfgFile string, watchInterval time.Duration) *Reloader {
	return &Reloader{
		reloadURL:     reloadURL,
		cfgFile:       cfgFile,
		watchInterval: watchInterval,
		retryInterval: 5 * time.Second,
	}
}

// We cannot detect everything via watch. Watch interval controls how often we re-read given dirs non-recursively.
func (r *Reloader) WithWatchInterval(duration time.Duration) {
	r.watchInterval = duration
}

// Watch starts to watch periodically the config file and rules and process them until the context
// gets canceled. Config file gets env expanded if cfgOutputFile is specified and reload is trigger if
// config or rules changed.
// Watch watchers periodically based on r.watchInterval.
// For config file it watches it directly as well via fsnotify.
// It watches rule dirs as well, but lot's of edge cases are missing, so rely on interval mostly.
func (r *Reloader) Watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return errors.Wrap(err, "create watcher")
	}
	defer watcher.Close()

	watchables := map[string]struct{}{}
	if r.cfgFile != "" {
		watchables[filepath.Dir(r.cfgFile)] = struct{}{}
		if err := watcher.Add(r.cfgFile); err != nil {
			return errors.Wrapf(err, "add config file %s to watcher", r.cfgFile)
		}

		if err := r.apply(ctx); err != nil {
			return err
		}
	}

	tick := time.NewTicker(r.watchInterval)
	defer tick.Stop()

	log.WithField("cfg", r.cfgFile).Info("started watching config for changes")

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case event := <-watcher.Events:
			if _, ok := watchables[filepath.Dir(event.Name)]; !ok {
				continue
			}
		case err := <-watcher.Errors:
			log.WithError(err).Error("watch error")
			continue
		}

		if err := r.apply(ctx); err != nil {
			// Critical error.
			return err
		}
	}
}

// apply triggers HTTP POST if file has changed in directory
func (r *Reloader) apply(ctx context.Context) error {
	var cfgHash []byte

	h := sha256.New()
	if r.cfgFile != "" {
		walkDir, err := filepath.EvalSymlinks(r.cfgFile)
		if err != nil {
			return errors.Wrap(err, "ruleDir symlink eval")
		}
		err = filepath.Walk(walkDir, func(path string, f os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// filepath.Walk uses Lstat to retriev os.FileInfo. Lstat does not
			// follow symlinks. Make sure to follow a symlink before checking
			// if it is a directory.
			targetFile, err := os.Stat(path)
			if err != nil {
				return err
			}

			if targetFile.IsDir() {
				return nil
			}

			if err := hashFile(h, path); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return errors.Wrap(err, "build hash")
		}
	}
	cfgHash = h.Sum(nil)
	if bytes.Equal(r.lastCfgHash, cfgHash) {
		return nil
	}

	// Retry trigger reload until it succeeded or next tick is near.
	// retryCtx, cancel := context.WithTimeout(ctx, r.watchInterval)
	// defer cancel()

	if err := r.triggerReload(ctx); err != nil {
		return errors.Wrap(err, "trigger reload")
	}

	r.lastCfgHash = cfgHash
	log.Info("Reload triggered")
	return nil
}

func hashFile(h hash.Hash, fn string) error {
	f, err := os.Open(fn)
	if err != nil {
		return err
	}

	if _, err := h.Write([]byte{'\xff'}); err != nil {
		return err
	}
	if _, err := h.Write([]byte(fn)); err != nil {
		return err
	}
	if _, err := h.Write([]byte{'\xff'}); err != nil {
		return err
	}

	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	return nil
}

func (r *Reloader) triggerReload(ctx context.Context) error {
	req, err := http.NewRequest("POST", r.reloadURL.String(), nil)
	if err != nil {
		return errors.Wrap(err, "create request")
	}
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "reload request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return errors.Errorf("received non-200 response: %s", resp.Status)
	}
	return nil
}
