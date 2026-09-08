# Changelog

This file documents the notable changes to `textreader`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

## v0.1.3 - 2026-08-09

No library changes. Repository maintenance only.

## v0.1.2 - 2026-08-08

### Changed

- Document every exported symbol in the `position` package, so godoc describes
  the full API.

## v0.1.1 - 2026-08-01

`v0.1.1` and `v0.1.0` name the same commit. The published files are identical
between the two tags. This release changes nothing.

## v0.1.0 - 2026-07-28

Initial release.

### Added

- Add `TextReader`, a buffered reader that tracks the line, column, and byte
  offset of the current read.
- Add `New()` and `NewWithCapacity()` to create a reader over any `io.Reader`.
- Add `ReadRune()` to read one UTF-8 rune and report its size in bytes.
- Add `Read()` to read an arbitrary byte chunk.
- Add `UnreadRune()` to unread the last rune. The reader supports one level of
  unread.
- Add `Seek()` to move the read position inside the buffered data.
- Add `Pos()` to return a copy of the current position.
- Add the `position` package, which tracks the line, column, and offset of a
  UTF-8 stream.
- Add rune-based column counting, so a multi-byte character reports its
  character position. `Offset()` still counts bytes.
