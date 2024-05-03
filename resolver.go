package bsrr

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	fds     map[string]*descriptorpb.FileDescriptorProto
	sources map[string][]byte
	mu      sync.RWMutex
}

type BufLockV1 struct {
	Version string                `json:"version,omitempty" yaml:"version,omitempty"`
	Deps    []BufLockDependencyV1 `json:"deps,omitempty" yaml:"deps,omitempty"`
}

type BufLockDependencyV1 struct {
	Remote     string    `json:"remote,omitempty" yaml:"remote,omitempty"`
	Owner      string    `json:"owner,omitempty" yaml:"owner,omitempty"`
	Repository string    `json:"repository,omitempty" yaml:"repository,omitempty"`
	Branch     string    `json:"branch,omitempty" yaml:"branch,omitempty"`
	Commit     string    `json:"commit,omitempty" yaml:"commit,omitempty"`
	Digest     string    `json:"digest,omitempty" yaml:"digest,omitempty"`
	CreateTime time.Time `json:"create_time,omitempty" yaml:"create_time,omitempty"`
}

type BufConfigV1 struct {
	Version string   `json:"version,omitempty" yaml:"version,omitempty"`
	Name    string   `json:"name,omitempty" yaml:"name,omitempty"`
	Deps    []string `json:"deps,omitempty" yaml:"deps,omitempty"`
}

type BufWorkV1 struct {
	Version     string   `json:"version,omitempty" yaml:"version,omitempty"`
	Directories []string `json:"directories,omitempty" yaml:"directories,omitempty"`
}

type Option func(*Resolver) error

var httpClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: time.Second,
		}).DialContext,
		TLSHandshakeTimeout: time.Second,
		IdleConnTimeout:     time.Second,
		MaxIdleConnsPerHost: 10,
	},
	Timeout: 5 * time.Second,
}

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

func BufLock(lockFile string) Option {
	return func(r *Resolver) error {
		if filepath.Base(lockFile) != bufLockFile {
			return fmt.Errorf("lock file should be %s", bufLockFile)
		}
		b, err := os.ReadFile(lockFile)
		if err != nil {
			return err
		}
		lock := BufLockV1{}
		if err := yaml.Unmarshal(b, &lock); err != nil {
			return err
		}
		if lock.Version != "v1" {
			return fmt.Errorf("unsupported lock file version")
		}
		for _, dep := range lock.Deps {
			commit := dep.Commit
			if dep.Branch != "" {
				commit = dep.Branch
			}
			module := fmt.Sprintf("%s/%s/%s", bufBuildHost, dep.Owner, dep.Repository)
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

func BufConfig(configFile string) Option {
	return func(r *Resolver) error {
		if filepath.Base(configFile) != bufConfigFile {
			return fmt.Errorf("config file should be %s", bufConfigFile)
		}
		b, err := os.ReadFile(configFile)
		if err != nil {
			return err
		}
		config := BufConfigV1{}
		if err := yaml.Unmarshal(b, &config); err != nil {
			return err
		}
		if config.Version != "v1" {
			return fmt.Errorf("unsupported lock file version")
		}
		opt := BufModule(config.Deps...)
		if err := opt(r); err != nil {
			return err
		}
		return nil
	}
}

func BufDir(dir string) Option {
	return func(r *Resolver) error {
		if _, err := os.Stat(filepath.Join(dir, bufLockFile)); err == nil {
			opt := BufLock(filepath.Join(dir, bufLockFile))
			if err := opt(r); err != nil {
				return err
			}
		} else if _, err := os.Stat(filepath.Join(dir, bufConfigFile)); err == nil {
			opt := BufConfig(filepath.Join(dir, bufConfigFile))
			if err := opt(r); err != nil {
				return err
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

		sources := map[string][]byte{}
		if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
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
}

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
	return paths
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

func fetchFileDescriptorSet(owner, repo, branchOrCommit string) (*descriptorpb.FileDescriptorSet, error) {
	var fdset descriptorpb.FileDescriptorSet
	u := fmt.Sprintf("https://%s/%s/%s/descriptor/%s", bufBuildHost, owner, repo, branchOrCommit)
	res, err := httpClient.Get(u)
	defer func() {
		_ = res.Body.Close()
	}()
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if err := proto.Unmarshal(b, &fdset); err != nil {
		return nil, err
	}
	return &fdset, nil
}
