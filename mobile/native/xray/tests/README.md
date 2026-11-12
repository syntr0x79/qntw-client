These are source overlays for the pinned Xray module, not a standalone package.
`mobile/scripts/prepare-xray-darwin.py` verifies and uses them inside the fork.
The nested module keeps normal client `go test ./...` from compiling the overlay
without the Xray implementation it tests. Its source and checksums are unchanged.
