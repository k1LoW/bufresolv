# Buf resolver for github.com/bufbuild/protocompile

## Usage

``` go
r, _ := bufresolv.New(bufresolv.BufDir("path/to/bofroot"))
comp := protocompile.Compiler{
	Resolver: protocompile.WithStandardImports(r),
}
fds, _ := comp.Compile(ctx, r.Paths()...)
```
