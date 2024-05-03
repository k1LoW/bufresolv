package bsrr

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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

type Resolver struct {
	fds map[string]*descriptorpb.FileDescriptorProto
	mu  sync.RWMutex
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
			if !strings.HasPrefix(module, "buf.build/") {
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
				// override if already exists
				r.fds[fd.GetName()] = fd
			}
		}
		return nil
	}
}

func BufLock(lockFile string) Option {
	return func(r *Resolver) error {
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
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, dep := range lock.Deps {
			commit := dep.Commit
			if dep.Branch != "" {
				commit = dep.Branch
			}
			fdset, err := fetchFileDescriptorSet(dep.Owner, dep.Repository, commit)
			if err != nil {
				return err
			}
			for _, fd := range fdset.GetFile() {
				// override if already exists
				r.fds[fd.GetName()] = fd
			}
		}
		return nil
	}
}

func BufConfig(configFile string) Option {
	return func(r *Resolver) error {
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

func New(opts ...Option) (*Resolver, error) {
	r := &Resolver{
		fds: map[string]*descriptorpb.FileDescriptorProto{},
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
	return paths
}

func (r *Resolver) FindFileByPath(path string) (protocompile.SearchResult, error) {
	result := protocompile.SearchResult{}
	r.mu.RLock()
	defer r.mu.RUnlock()
	fd, ok := r.fds[path]
	if !ok {
		return result, protoregistry.NotFound
	}
	result.Proto = fd
	return result, nil
}

func fetchFileDescriptorSet(owner, repo, branchOrCommit string) (*descriptorpb.FileDescriptorSet, error) {
	var fdset descriptorpb.FileDescriptorSet
	u := fmt.Sprintf("https://buf.build/%s/%s/descriptor/%s", owner, repo, branchOrCommit)
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
