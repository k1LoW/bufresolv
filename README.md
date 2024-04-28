# Buf Schema Registory Resolver for github.com/bufbuild/protocompile

## Usage

``` go
br, _ := bsrr.New(bsrr.BufLock("path/to/buf.lock"))
comp := protocompile.Compiler{
	Resolver: protocompile.WithStandardImports(br),
}
fds, _ := comp.Compile(ctx, protos...)
```
