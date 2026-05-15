// Package omp implements an adapter for Oh My Pi (OMP) agent sessions.
//
// OMP is a fork of Pi Agent and uses the same JSONL format. Sessions are stored
// in ~/.omp/agent/sessions/<encoded-path>/ where encoded-path is the project
// path with slashes replaced by dashes, wrapped in double dashes.
package omp
