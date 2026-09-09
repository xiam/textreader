# Contributing

Thanks for your interest in improving `textreader`! Contributions are welcome.

## How to contribute

1. Fork the repository and create a branch for your change.
2. Make your change, keeping it focused and well-scoped.
3. Ensure the test suite passes locally:

   ```sh
   go vet ./...
   make test
   ```

4. Open a pull request against `master` describing what you changed and why.

The maintainer reviews incoming pull requests and imports accepted changes into
the project. Your authorship is always preserved — commits keep their original
author, and when a change is reworked during import, contributors are credited
with a `Co-authored-by:` trailer.

## What makes a smooth review

- **Small, focused PRs** are easier to review and land quickly.
- **Tests** for new behavior or bug fixes help a lot.
- **Idiomatic Go** — run `gofmt` and `go vet` before submitting.
- A clear description of the problem and your approach.

## A note on build and CI changes

The library aims to stay small and dependency-light. Pull requests that modify
CI, build, or workflow configuration, or that introduce hidden network or build
steps, may be declined or reworked. If you believe a change to the project's
tooling is warranted, please open an issue to discuss it first so we can agree
on the approach before you invest time in a PR.

Part of this is enforced automatically. The `policy-gate` check fails a pull
request that touches any of:

- a path listed under `blocked_paths` in `publish-policy.json`;
- `publish-policy.json` itself;
- `.github/workflows/` or `.github/publish-policy/` — the check's own code.

The check reads both the policy and its own code from the base branch and never
checks out or runs anything from the pull request, so a PR cannot switch off the
check that judges it. If your change genuinely needs one of those paths, open an
issue first and a maintainer will apply it separately.

## Reporting issues

Found a bug or have a feature idea? Please open an issue with a clear
description and, for bugs, a minimal reproduction if you can.

## License

By contributing, you agree that your contributions will be licensed under the
same terms as the project (see `LICENSE.md`).
