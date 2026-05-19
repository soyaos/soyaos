// Package store is SoyaOS Solo's on-disk persistence layer.
//
// Solo edition's design promise is "all data on the local machine, nothing
// leaves the device" (see specs/cli/v0.md and the storage-dependencies
// design doc). The pre-v0.1.0-alpha shipping shape kept API keys, scheduled
// jobs, memory entries and artifact bytes in process memory only — restart
// blew everything away. This package replaces those with a Bolt-backed
// key/value store rooted at `--data-dir`.
//
// Layout:
//
//   <data-dir>/
//     soyaos.bolt          // single bbolt database, one bucket per namespace
//
// Namespaces in use:
//   - "auth.keys"          API keys keyed by raw `sk-soya-...`
//   - "scheduler.jobs"     time-wheel jobs keyed by Job.ID
//   - "memory.entries"     memory KV entries keyed by length-prefix composite
//   - "artifact.index"     artifact metadata keyed by SnapshotHash
//
// The Store interface is intentionally byte-in / byte-out. Each consumer
// owns its own (de)serialization. CompositeKey() is provided for callers
// that need injection-safe multi-part keys (e.g. memory's
// `<scope>·<owner>·<key>`).
//
// Cluster / Cloud editions will substitute a server-backed implementation
// behind the same interface; only `Open` will change.
package store
