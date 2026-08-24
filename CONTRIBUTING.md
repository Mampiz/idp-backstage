# Contributing

The full guide — repository layout, how to run the tests, what each end-to-end
check actually proves, and how CI is split — lives in
**[docs/contributing.md](docs/contributing.md)**.

The short version:

```bash
make test                    # Go unit tests, both modules
cd backstage && yarn backstage-cli repo test
make verify-f0               # end-to-end against a real cluster
make help                    # every target
```

Two things worth knowing before you change anything:

- The end-to-end checks **create real GitHub repositories** in the configured
  account. Run them knowingly.
- [webapp-operator](https://github.com/Mampiz/webapp-operator) is consumed as a
  dependency and never modified from here. Where it has gaps they are worked
  around on this side and documented, with the upstream fix named.

By contributing you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
