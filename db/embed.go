// Package db exposes the Goose migration and seed data trees as embedded
// filesystems so the single HANGAR binary needs no files on disk at runtime
// (SRS §9.1 — Docker Compose pulls a pre-built image; nothing is compiled or
// read from a source checkout in production).
//
// Both directories are empty except for a .gitkeep placeholder until Phase 1a
// (migrations) and later phases (seed data) populate them; `all:` is used so
// the embed succeeds against an otherwise-empty directory.
package db

import "embed"

//go:embed all:migrations
var Migrations embed.FS

//go:embed all:seed
var Seed embed.FS
