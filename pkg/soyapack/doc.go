// Package soyapack is the canonical SoyaPack v0 manifest types + loader +
// validator. It is the in-process implementation of the contract specified at
// https://github.com/soyaos/specs/blob/main/specs/soyapack/v0/manifest.md.
//
// A SoyaPack is the **atomic delivery unit** for anything that runs on
// SoyaOS — an Agent, a Skill, or a Memory Pack. Every Pack carries a
// `soyapack.yaml` manifest validated by this package.
//
// Strictness rules (from manifest.md):
//
//   - `spec_version` must equal "soyapack.v0". Other values are rejected.
//   - `kind` must be one of "Skill" / "Agent" / "Memory".
//   - Unknown top-level fields are rejected unless prefixed with `x-`
//     (RFC-7807-style extension namespace). Extension keys are kept in
//     Manifest.Extensions for passthrough.
//
// External Agent authors should NOT import pkg/factory directly. They should
// import this package; pkg/factory is reserved for the NL→Manifest
// translator (Agent Factory, DD-009) and references Manifest from here.
package soyapack
