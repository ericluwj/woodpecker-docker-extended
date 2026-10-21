# Third-party notices

Reference normalization and associated test cases are adapted from
[ericluwj/woodpecker-apple-container](https://github.com/ericluwj/woodpecker-apple-container),
`backend/apple/images.go` and `images_test.go`, commit
`fce2777a36cd93b1388ef4ef80fc0d2ea44b0fbe`.
Copyright (c) 2026 Eric Lu; MIT license (included as LICENSE).

This agent links Woodpecker v3.19.0, copyright Woodpecker Authors, under the
Apache License 2.0. Its license is included in `licenses/woodpecker-LICENSE`.
Docker connection configuration follows that version's Docker backend.
The backend execution code is imported, rather than copied or forked.

Other transitive Go dependencies retain their own licenses in their source
modules. Inspect the pinned dependency graph with `go list -m all`.
