package helmrepo

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cache is the reusable, concurrency-safe front end to Loader. Every read of
// the Helm releases list goes through it (app's lister decorator, and the
// history screen), so it must be cheap to call repeatedly and safe from more
// than one tea.Cmd goroutine at a time.
//
// Cheap means: each call stats the config file and the cache directory's
// index files and compares a signature of names/sizes/mtimes. Only a changed
// signature re-parses — which matters because the index files are the
// largest thing kute reads anywhere (6 MB for prometheus-community alone),
// and a Helm list re-runs on every release Secret change event.
type Cache struct {
	loader Loader

	mu sync.Mutex
	// static short-circuits every disk read: the demo cluster's chart
	// versions are fixtures, not the machine's own Helm setup, and --demo
	// must render the same on any machine.
	static    bool
	loaded    bool
	signature string
	index     Index
	// parses counts full re-parses, for the test that proves an unchanged
	// cache doesn't pay for one twice.
	parses int
}

// NewCache builds a Cache over loader (the zero Loader discovers Helm's own
// default paths).
func NewCache(loader Loader) *Cache { return &Cache{loader: loader} }

// NewStaticCache builds a Cache from a fixed chart→version map attributed to
// one repo, reading no disk at all — how --demo gets a repo cache that looks
// the same on every machine, and how a test gets one without a temp dir.
func NewStaticCache(repo string, charts map[string]string) *Cache {
	idx := Index{
		charts: make(map[string][]Chart, len(charts)),
		status: Status{Configured: true, Repos: 1, Charts: len(charts), Oldest: time.Now()},
	}
	for name, version := range charts {
		idx.charts[name] = []Chart{{Version: version, Repo: repo}}
	}
	return &Cache{static: true, loaded: true, index: idx}
}

// Index returns the current view of the repo cache, re-parsing only when the
// files on disk have changed since the last call. A parse error yields the
// empty Index — an unreadable Helm setup is a "we don't know", never a
// failure of whatever screen asked.
func (c *Cache) Index() Index {
	if c == nil {
		return Index{charts: map[string][]Chart{}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.static {
		return c.index
	}
	sig := c.loader.signature()
	if c.loaded && sig == c.signature {
		return c.index
	}
	idx, err := c.loader.Load()
	if err != nil {
		idx = Index{charts: map[string][]Chart{}}
	}
	c.loaded, c.signature, c.index = true, sig, idx
	c.parses++
	return c.index
}

// signature is a cheap fingerprint of everything Load would read: the config
// file plus every *-index.yaml in the cache dir, by name, size and mtime. It
// deliberately covers files Load might *start* reading (a newly added repo),
// not just the ones it read last time.
func (l Loader) signature() string {
	configPath := l.ConfigPath
	if configPath == "" {
		configPath = defaultConfigPath()
	}
	cachePath := l.CachePath
	if cachePath == "" {
		cachePath = defaultCachePath()
	}

	var b strings.Builder
	if info, err := os.Stat(configPath); err == nil {
		writeStat(&b, configPath, info)
	}
	entries, err := os.ReadDir(cachePath)
	if err != nil {
		return b.String()
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "-index.yaml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // ReadDir is already sorted, but the signature must not depend on that
	for _, name := range names {
		info, err := os.Stat(filepath.Join(cachePath, name))
		if err != nil {
			continue
		}
		writeStat(&b, name, info)
	}
	return b.String()
}

func writeStat(b *strings.Builder, name string, info os.FileInfo) {
	b.WriteString(name)
	b.WriteByte(':')
	b.WriteString(info.ModTime().UTC().Format("20060102150405.000000000"))
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(info.Size(), 10))
	b.WriteByte('\n')
}
