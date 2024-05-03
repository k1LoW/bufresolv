# Buf Schema Registory Resolver for github.com/bufbuild/protocompile

## Usage

``` go
r, _ := bsrr.New(bsrr.BufDir("path/to/bofroot"))
comp := protocompile.Compiler{
	Resolver: protocompile.WithStandardImports(r),
}
fds, _ := comp.Compile(ctx, r.Paths()...)
```
