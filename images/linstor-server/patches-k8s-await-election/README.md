# Patches for k8s-await-election

This directory contains patches for the k8s-await-election component.

## How to add patches

1. Place `.patch` files in this directory
2. Patches are applied in alphabetical order
3. Use naming convention: `001-description.patch`, `002-description.patch`, etc.

## Creating patches

```bash
git diff > 001-my-fix.patch
```

or for specific files:

```bash
git diff path/to/file > 001-my-fix.patch
```
