# Buf resolver for github.com/bufbuild/protocompile [![Go Reference](https://pkg.go.dev/badge/github.com/k1LoW/bufresolv.svg)](https://pkg.go.dev/github.com/k1LoW/bufresolv) [![build](https://github.com/k1LoW/bufresolv/actions/workflows/ci.yml/badge.svg)](https://github.com/k1LoW/bufresolv/actions/workflows/ci.yml) ![Coverage](https://raw.githubusercontent.com/k1LoW/octocovs/main/badges/k1LoW/bufresolv/coverage.svg) ![Code to Test Ratio](https://raw.githubusercontent.com/k1LoW/octocovs/main/badges/k1LoW/bufresolv/ratio.svg) ![Test Execution Time](https://raw.githubusercontent.com/k1LoW/octocovs/main/badges/k1LoW/bufresolv/time.svg)

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
