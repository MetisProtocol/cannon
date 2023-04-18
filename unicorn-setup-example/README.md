# Unicorn Example Application


This setup guide will ensure that you have the right devtools to work on cannon.

Dependencies
- unicorn
- golang

## Installing Unicorn

Unicorn can be installed from source by following their instructions
[here](https://github.com/unicorn-engine/unicorn/blob/master/docs/COMPILE.md). Note that you will have to run
`sudo make install` to fully install it locally.

On mac it can also be installed with `brew install unicorn`. If you do that, you will have to export the DYLD_PATH.
See upstream's directions [here](https://www.unicorn-engine.org/docs/).

## Installing golang

Because `unicorn` is a `C` project, we must use `cgo`. Cross compiling cgo is possible, but we do not support it.
If you see an error like
`Ignoring file /usr/local/Cellar/unicorn/2.0.0/lib/libunicorn.dylib, building for macOS-x86_64 but attempting to link with file built for macOS-arm64`
check that the architecture in `go version` is that same as `arch`. Go disables cross compiles when cgo is active & it defaults to it's internal
architrecture.


## Working with Docker

Unicorn-engine is being added as a package to debian, however it has not made it to stable so we use the testing package.
We have to copy the library as well.


## Veriying you setup

Local Setup
```
% go run .
EAX is now: 1234
```

Docker Setup
```
docker build . -f Dockerfile.unicorn -t local:unicorn
% docker run local:unicorn
EAX is now: 1234
```

## Uninstalling Unicorn

Files to remove if manually installed.
```
/usr/local/lib/libunicorn.2.dylib
/usr/local/lib/libunicorn.dylib
/usr/local/lib/libunicorn.a
/usr/local/include/unicorn/arm.h
/usr/local/include/unicorn/arm64.h
/usr/local/include/unicorn/m68k.h
/usr/local/include/unicorn/mips.h
/usr/local/include/unicorn/platform.h
/usr/local/include/unicorn/ppc.h
/usr/local/include/unicorn/riscv.h
/usr/local/include/unicorn/s390x.h
/usr/local/include/unicorn/sparc.h
/usr/local/include/unicorn/tricore.h
/usr/local/include/unicorn/unicorn.h
/usr/local/include/unicorn/x86.h
/usr/local/lib/pkgconfig/unicorn.pc
```