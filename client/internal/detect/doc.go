// Package detect reports whether the microphone and the webcam are in use.
//
// The Windows implementation polls the CapabilityAccessManager consent store
// in the registry (CLAUDE.md §5.2) without per-tick allocations; a fake
// implementation lets the rest of the app build and be tested on Linux.
package detect
