# Buf Schema Registory Resolver for github.com/bufbuild/protocompile

## Usage

``` go
comp := protocompile.Compiler{
	Resolver: protocompile.WithStandardImports(bsrr.New(bssr.BufLock("path/to/buf.lock"))),
}
fds, err := comp.Compile(ctx, protos...)
```