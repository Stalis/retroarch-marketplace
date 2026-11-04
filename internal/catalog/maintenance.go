package catalog

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"retroarch-marketplace/internal/content"
)

// RunMaintenance periodically checks sources and removes expired generated archives.
// It returns when ctx is canceled.
func (s *Server) RunMaintenance(ctx context.Context, scanInterval, cleanupInterval, cacheMaxAge time.Duration) {
	if scanInterval <= 0 || cleanupInterval <= 0 || cacheMaxAge <= 0 {
		log.Print("catalog maintenance: intervals and cache age must be positive")
		<-ctx.Done()
		return
	}
	scan := time.NewTicker(scanInterval)
	defer scan.Stop()
	cleanup := time.NewTicker(cleanupInterval)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-scan.C:
			if err := s.checkSources(); err != nil {
				log.Printf("catalog source scan: %v", err)
			}
		case <-cleanup.C:
			if err := s.cleanupCache(time.Now(), cacheMaxAge); err != nil {
				log.Printf("catalog cache cleanup: %v", err)
			}
		}
	}
}

func (s *Server) checkSources() error {
	_, err := s.load()
	s.mu.Lock()
	s.scanErr = err
	s.mu.Unlock()
	return err
}

// The lease keeps a cache entry alive until the response has finished streaming.
func (s *Server) archive(fingerprint string, item content.Item) (string, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	path, err := s.archiveLocked(fingerprint, item)
	if err != nil {
		return "", err
	}
	if s.active == nil {
		s.active = make(map[string]int)
	}
	s.active[path]++
	return path, nil
}

func (s *Server) releaseArchive(path string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.active[path]--
	if s.active[path] == 0 {
		delete(s.active, path)
	}
}

func cacheName(name string) bool {
	if len(name) != 133 || name[64] != '-' || name[129:] != ".zip" {
		return false
	}
	for i := range 129 {
		if i == 64 {
			continue
		}
		c := name[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// cacheDir refuses symlink roots; a missing cache is normal.
func (s *Server) cacheDir() (string, error) {
	for _, dir := range []string{s.packagesDir, filepath.Join(s.packagesDir, ".cache")} {
		info, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect %s: %w", dir, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s is not a directory", dir)
		}
	}
	return filepath.Join(s.packagesDir, ".cache"), nil
}

func (s *Server) cleanupCache(now time.Time, maxAge time.Duration) error {
	s.cacheMu.Lock()
	dir, err := s.cacheDir()
	if err != nil || dir == "" {
		s.cacheMu.Unlock()
		return err
	}
	entries, err := os.ReadDir(dir)
	s.cacheMu.Unlock()
	if err != nil {
		return fmt.Errorf("read cache %s: %w", dir, err)
	}
	var failures []error
	for _, entry := range entries {
		if !cacheName(entry.Name()) {
			continue
		}
		s.cacheMu.Lock()
		currentDir, err := s.cacheDir()
		if err == nil && currentDir != "" {
			path := filepath.Join(currentDir, entry.Name())
			if s.active[path] == 0 {
				info, statErr := os.Lstat(path)
				if statErr == nil && info.Mode().IsRegular() && info.ModTime().Before(now.Add(-maxAge)) {
					statErr = os.Remove(path)
				}
				if statErr != nil && !os.IsNotExist(statErr) {
					err = fmt.Errorf("remove expired cache entry %s: %w", path, statErr)
				}
			}
		}
		s.cacheMu.Unlock()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
