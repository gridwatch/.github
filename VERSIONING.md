# Versioning Policy

> This document describes the Versioning Policy for [@gridwatch](https://github.com/gridwatch).

## Table of Contents

<!-- TOC -->
* [Versioning Policy](#versioning-policy)
  * [Table of Contents](#table-of-contents)
  * [Terraform Core](#terraform-core)
  * [Terraform Providers](#terraform-providers)
  * [GitHub Actions](#github-actions)
  * [Container Images and Releases](#container-images-and-releases)
  * [Language Runtimes](#language-runtimes)
<!-- TOC -->

The [@gridwatch](https://github.com/gridwatch) project aims to deliver code that results in a predictable and reproducible outcome.

Version-pinning is a foundational aspect of this goal.

## Terraform Core

> **Note**
>
> This section applies to Terraform Core (the `terraform` binary).

Terraform Core is pinned to a **major-version range** with a specific minor-version floor:

* starts at the minor-version release currently adopted across all workspaces (e.g. `>= 1.14.0`)
* ends by excluding the next major-version release (e.g. `< 2.0.0`)

```hcl
required_version = ">= 1.14.0, < 2.0.0"
```

This range is declared in the `terraform` stanza of every `terraform.tf`.

## Terraform Providers

> **Note**
>
> This section applies to Terraform Providers.

Providers are pinned to a **major-version range** with a specific patch-version floor:

* starts at an exact known-good patch release (e.g. `>= 6.13.0`)
* ends by excluding the next major-version release (e.g. `< 7.0.0`)
* **uses `>=`, never `~>`** — the pessimistic operator masks intentional major-version holds

```hcl
required_providers {
  # see https://registry.terraform.io/providers/integrations/github/6.13.0/docs
  github = {
    source  = "integrations/github"
    version = ">= 6.13.0, < 7.0.0"
  }
}
```

Beta, release-candidate, and pre-release providers are **not** used — work around bugs in stable releases instead.

## GitHub Actions

Actions are pinned to **exact tag versions** and accompanied by a `# see` comment linking to the release notes:

```yaml
- name: Checkout
  # see https://github.com/actions/checkout/releases/tag/v7.0.0
  uses: actions/checkout@v7.0.0
```

Version constants live in [`infrastructure-github/locals.tf`](https://github.com/gridwatch/infrastructure-github/blob/main/locals.tf) and are templated into workflow files via Terraform.

`actions/checkout@v4` is prohibited — use `v6` or later.

## Container Images and Releases

Container images and release tags use **date-based versions**, not semantic versioning:

```text
vYYYYMMDD-N
```

where `N` is a sequential build number for that day (e.g. `v20260321-1`, `v20260321-2`). The `v` prefix is required on both git tags and container image tags.

Never push `latest` to a registry. Always use an explicit `vYYYYMMDD-N` tag.

## Language Runtimes

| Runtime            | Minimum Version |
|--------------------|-----------------|
| Go                 | 1.26            |
| Swift              | 6               |
| iOS / iPadOS       | 26              |
| macOS (build host) | 26              |
| Node.js            | 22              |
| Hugo               | 0.159.1         |
