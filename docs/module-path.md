# Go Module Path Specification & Casing

## Overview

Issue [#380](https://github.com/Savitura/Fluxa/issues/380) requested reviewing the Go module path in `go.mod` relative to the repository path (`github.com/Savitura/Fluxa`).

## Casing Constraints in Go Modules

The Go module system enforces strict casing rules as specified in the [Go Module Reference](https://go.dev/ref/mod#module-path):

1. **Case-Insensitive Filesystem Safety**: To prevent package cache collisions on case-insensitive filesystems (such as macOS APFS and Windows NTFS), the Go toolchain case-encodes any uppercase letters in module paths using exclamation points (e.g. `github.com/!savitura/!fluxa`).
2. **Proxy and Checksum Database Collisions**: Uppercase characters in module paths create subtle incompatibilities with module proxies and checksum databases (`proxy.golang.org`, `sum.golang.org`).
3. **Module Path Permanence**: In accordance with Go best practices, changing module paths in a multi-package repository across hundreds of files introduces breaking import churn for all consuming services and external callers.

## Decision

The module path is preserved as `github.com/fluxa/fluxa`. When external vanity or org URL redirects are formalized, lowercase paths (e.g. `github.com/savitura/fluxa`) are preferred over mixed-case paths (`github.com/Savitura/Fluxa`).
