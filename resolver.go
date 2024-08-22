package bufresolv

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"gopkg.in/yaml.v3"
)

var _ protocompile.Resolver = (*Resolver)(nil)

const (
	bufBuildHost  = "buf.build"
	bufLockFile   = "buf.lock"
	bufConfigFile = "buf.yaml"
	bufWorkFile   = "buf.work.yaml"
)

type Resolver struct {
	version string
	fds     map[string]*descriptorpb.FileDescriptorProto
	sources map[string][]byte
	mu      sync.RWMutex
}

type BufLockV1V2 struct {
	Version string           `yaml:"version,omitempty"`
	Deps    []BufLockDepV1V2 `yaml:"deps,omitempty"`
}

type BufLockDepV1V2 struct {
	NameV2     string `yaml:"name,omitempty"`
	Remote     string `yaml:"remote,omitempty"`
	Owner      string `yaml:"owner,omitempty"`
	Repository string `yaml:"repository,omitempty"`
	Branch     string `yaml:"branch,omitempty"`
	Commit     string `yaml:"commit,omitempty"`
}

type BufConfigV1V2 struct {
	Version string              `yaml:"version,omitempty"`
	Name    string              `yaml:"name,omitempty"`
	Modules []BufConfigModuleV2 `yaml:"modules,omitempty"`
	Deps    []string            `yaml:"deps,omitempty"`
}

type BufWorkV1 struct {
	Version     string   `yaml:"version,omitempty"`
	Directories []string `yaml:"directories,omitempty"`
}

type BufConfigModuleV2 struct {
	Path string `yaml:"path"`
}

type Option func(*Resolver) error

var httpClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			KeepAlive: 5 * time.Minute,
		}).DialContext,
		TLSHandshakeTimeout: time.Second,
		IdleConnTimeout:     time.Second,
		MaxIdleConnsPerHost: 10,
	},
	Timeout: 5 * time.Second,
}

// BufModule fetches the file descriptor set from buf.build.
func BufModule(modules ...string) Option {
	return func(r *Resolver) error {
		for _, module := range modules {
			if !strings.HasPrefix(module, bufBuildHost) {
				return fmt.Errorf("remote should be buf.build")
			}
			splitted := strings.Split(module, "/")
			if len(splitted) != 3 && !(len(splitted) == 5 && splitted[3] == "tree") {
				return fmt.Errorf("module should be in format <remote>/<owner>/<repository>[/tree/<branch or commit>]: %s", module)
			}
			b := "main"
			if len(splitted) == 5 {
				b = splitted[4]
			}
			fdset, err := fetchFileDescriptorSet(splitted[1], splitted[2], b)
			if err != nil {
				return err
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			for _, fd := range fdset.GetFile() {
				if _, ok := r.fds[fd.GetName()]; ok && len(splitted) == 5 {
					continue
				}
				// override if branch or commit is specified.
				r.fds[fd.GetName()] = fd
			}
		}
		return nil
	}
}

// BufLock reads the lock file and fetches the file descriptor set from buf.build.
// Supported versions are v1 and v2.
func BufLock(lockFile string) Option {
	return func(r *Resolver) error {
		if filepath.Base(lockFile) != bufLockFile {
			return fmt.Errorf("lock file should be %s", bufLockFile)
		}
		b, err := os.ReadFile(lockFile)
		if err != nil {
			return err
		}
		lock := BufLockV1V2{}
		if err := yaml.Unmarshal(b, &lock); err != nil {
			return err
		}
		if lock.Version != "v1" && lock.Version != "v2" {
			return fmt.Errorf("unsupported lock file version")
		}
		r.version = lock.Version
		for _, dep := range lock.Deps {
			if (dep.Remote == "" || dep.Owner == "" || dep.Repository == "") && dep.NameV2 == "" {
				return fmt.Errorf("remote/owner/repotitory or name is required")
			}
			commit := dep.Commit
			if dep.Branch != "" {
				commit = dep.Branch
			}
			owner := dep.Owner
			repository := dep.Repository
			if dep.NameV2 != "" {
				splitted := strings.Split(dep.NameV2, "/")
				if len(splitted) != 3 {
					return fmt.Errorf("name should be in format <remote>/<owner>/<repository>: %s", dep.NameV2)
				}
				owner = splitted[1]
				repository = splitted[2]
			}
			module := fmt.Sprintf("%s/%s/%s", bufBuildHost, owner, repository)
			if commit != "" {
				module = fmt.Sprintf("%s/tree/%s", module, commit)
			}
			opt := BufModule(module)
			if err := opt(r); err != nil {
				return err
			}
		}
		return nil
	}
}

// BufConfig reads the config file and fetches the file descriptor set from buf.build.
// Supported versions are v1 and v2.
func BufConfig(configFile string) Option {
	return func(r *Resolver) error {
		if filepath.Base(configFile) != bufConfigFile {
			return fmt.Errorf("config file should be %s", bufConfigFile)
		}
		b, err := os.ReadFile(configFile)
		if err != nil {
			return err
		}
		config := BufConfigV1V2{}
		if err := yaml.Unmarshal(b, &config); err != nil {
			return err
		}
		if config.Version != "v1" && config.Version != "v2" {
			return fmt.Errorf("unsupported lock file version")
		}
		r.version = config.Version
		opt := BufModule(config.Deps...)
		if err := opt(r); err != nil {
			return err
		}
		if config.Version != "v2" {
			return nil
		}
		root := filepath.Dir(configFile)
		if len(config.Modules) == 0 {
			if err := r.walkDir(root); err != nil {
				return err
			}
		}
		for _, m := range config.Modules {
			if err := r.walkDir(filepath.Join(root, m.Path)); err != nil {
				return err
			}
		}
		return nil
	}
}

// BufWork reads the work file and fetches the file descriptor set from buf.build.
// Supported versions are v1 and v2.
func BufDir(dir string) Option {
	return func(r *Resolver) error {
		if _, err := os.Stat(filepath.Join(dir, bufConfigFile)); err == nil {
			opt := BufConfig(filepath.Join(dir, bufConfigFile))
			if err := opt(r); err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(dir, bufLockFile)); err == nil {
				opt := BufLock(filepath.Join(dir, bufLockFile))
				if err := opt(r); err != nil {
					return err
				}
			}
			if r.version == "v2" {
				return nil
			}
		} else if _, err := os.Stat(filepath.Join(dir, bufLockFile)); err == nil {
			opt := BufLock(filepath.Join(dir, bufLockFile))
			if err := opt(r); err != nil {
				return err
			}
			if r.version == "v2" {
				return nil
			}
		} else if _, err := os.Stat(filepath.Join(dir, bufWorkFile)); err == nil {
			b, err := os.ReadFile(filepath.Join(dir, bufWorkFile))
			if err != nil {
				return err
			}
			work := BufWorkV1{}
			if err := yaml.Unmarshal(b, &work); err != nil {
				return err
			}
			if work.Version != "v1" {
				return fmt.Errorf("unsupported work file version")
			}
			for _, wd := range work.Directories {
				opt := BufDir(filepath.Join(dir, wd))
				if err := opt(r); err != nil {
					return err
				}
			}
			return nil
		} else {
			return fmt.Errorf("no buf.lock, buf.yaml or buf.work.yaml file found in %s", dir)
		}

		// Load local proto files.
		if err := r.walkDir(dir); err != nil {
			return err
		}

		return nil
	}
}

// New creates a new resolver.
func New(opts ...Option) (*Resolver, error) {
	r := &Resolver{
		fds:     map[string]*descriptorpb.FileDescriptorProto{},
		sources: map[string][]byte{},
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Paths returns the paths of the file descriptor sets and sources.
func (r *Resolver) Paths() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	paths := make([]string, 0, len(r.fds))
	for path := range r.fds {
		paths = append(paths, path)
	}
	for path := range r.sources {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func (r *Resolver) FindFileByPath(path string) (protocompile.SearchResult, error) {
	result := protocompile.SearchResult{}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if b, ok := r.sources[path]; ok {
		result.Source = bytes.NewReader(b)
		return result, nil
	}
	fd, ok := r.fds[path]
	if ok {
		result.Proto = fd
		return result, nil
	}
	return result, protoregistry.NotFound
}

func (r *Resolver) walkDir(dir string) error {
	sources := map[string][]byte{}
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".proto" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		sources[rel] = b
		return nil
	}); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for path, b := range sources {
		r.sources[path] = b
	}
	return nil
}

func fetchFileDescriptorSet(owner, repo, branchOrCommit string) (*descriptorpb.FileDescriptorSet, error) {
	var fdset descriptorpb.FileDescriptorSet
	u := fmt.Sprintf("https://%s/%s/%s/descriptor/%s", bufBuildHost, owner, repo, branchOrCommit)
	res, err := httpClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = res.Body.Close()
	}()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if err := proto.Unmarshal(b, &fdset); err != nil {
		return nil, err
	}
	return &fdset, nil
}
