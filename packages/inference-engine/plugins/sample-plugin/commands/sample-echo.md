---
name: sample-echo
version: 0.1.0
description: Echo back whatever arguments are passed to the bundled plugin command.
argument-hint: text to echo
allowed-tools:
  - bash
---

Echo the following text exactly: $ARGUMENTS
