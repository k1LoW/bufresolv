# Buf resolver for github.com/bufbuild/protocompile

## Usage

``` go
r, _ := bufresolv.New(bufresolv.BufDir("path/to/bofroot"))
comp := protocompile.Compiler{
	Resolver: protocompile.WithStandardImports(r),
}
fds, _ := comp.Compile(ctx, r.Paths()...)
```

## References

- [bufbuild/buf](https://github.com/bufbuild/buf): The best way of working with Protocol Buffers.
