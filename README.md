# VSDAW

OpenDAW-powered digital audio workstation inside Visual Studio Code.

> **License:** AGPL-3.0-or-later  
> VSDAW is built on top of the [OpenDAW](https://github.com/andremichelle/openDAW) SDK, which is licensed under AGPL-3.0-or-later.

## Features

- First-class VS Code tabs: Timeline, Mixer, Piano Roll, Browser, and Graph.
- Custom `.vsdaw` project files stored as OpenDAW-compatible ZIP bundles.
- Audio and MIDI recording with multi-take support.
- OpenDAW stock devices for effects and instruments.
- Offline export to WAV, FLAC, and OGG.
- Native VS Code theming and keyboard shortcuts.

## Development

```bash
bun install
bun run build
bun run test
bun run package
```

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).
