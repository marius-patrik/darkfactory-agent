# Commands

## Setup

```powershell
npm ci
```

## Development

```powershell
npm run dev
```

## Validation

```powershell
npm run check
npm run typecheck
npm test
npm run build
```

## CI

GitHub Actions runs:

```powershell
npm ci
npm run check
```

## Managed Sync

```powershell
npm run install:url
npm run sync:managed
```

## Product integration

DarkFactory has no component release command. Agent OS root integration owns
versioning and publication.
