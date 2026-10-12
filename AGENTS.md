# CLAUDE.md

This file provides guidance to AI coding agents — Claude Code (claude.ai/code) and vendor-neutral tools such as Codex, OpenCode, Cursor, and Copilot — when working with code in this repository.

## Agent instruction files

`CLAUDE.md` and `AGENTS.md` are kept **byte-identical**. `CLAUDE.md` is what Claude Code reads; `AGENTS.md` is what vendor-neutral agent tools read — Codex, OpenCode, Cursor, Copilot, and whatever follows them. Two real files, deliberately not a symlink: not every tool resolves one.

**After editing either file, copy it over the other — don't repeat the edit by hand:**

```bash
cp CLAUDE.md AGENTS.md   # or the reverse, whichever you just edited
```

Retyping a change is exactly how the two drift; one reflowed line or reworded clause is enough. `diff CLAUDE.md AGENTS.md` must print nothing. If it ever does, treat it as a defect and fix it by letting one file win wholesale — never by merging them.

## What this repo is

A first-party **OpenTofu / Terraform provider for [Hetzner Robot](https://robot.hetzner.com)** — the dedicated-server side of Hetzner — owned by kirchDev. It covers the complete [Robot webservice](https://robot.hetzner.com/doc/webservice/en.html): servers, IPs, subnets, failover, reverse DNS, SSH keys, boot configurations, resets, firewalls and templates, vSwitches, the inclusive Storage Box, orders, products and traffic. Hetzner Cloud (and every Storage Box other than the inclusive BX10) belongs to the official `hcloud` provider, not here.

- **Provider type (HCL):** `hetznerrobot` → `provider "hetznerrobot" {}`
- **OpenTofu registry address:** `kirchdev/hetznerrobot`
- **Go module:** `github.com/kirchDev/terraform-provider-hetznerrobot`
- **SDK:** `terraform-plugin-framework` (NOT the legacy SDKv2)

The repo name format `NAMESPACE/terraform-provider-NAME` is **mandatory** for the OpenTofu/Terraform registry — it's not a style choice.

**Deliberately left out:** `GET /boot/{n}/rescue/last` and `/linux/last` (they only repeat a password the boot resource already holds), the order transaction listings (a 30-day history, not infrastructure), the Storage Box snapshot revert (a destructive one-off), `GET /wol/{n}` (only echoes addresses). Don't add them without a conversation.

## Current state

27 resources and 36 data sources, every documented endpoint mapped. Verification differs by area — the README's _Test status_ table is the user-facing version of this list, keep the two in step:

- **Live-verified (2026-10)** on an account with one auction server — `TestLive*` in `live_test.go`: every account-wide data source; `hetznerrobot_server`, `_ip`, `_subnet` (adopt, change, restore, import); `_rdns`, `_ssh_key`, `_firewall_template`, `_vswitch` (full lifecycle); `_firewall` (adopt keeps the rules, replace, import — a Robot firewall apply takes about a minute); `_boot_rescue` + `_server_reset` (hardware reset into the Rescue System; Robot cleared `active` about 30 s later and the plan stayed empty); `_wake_on_lan`; the boot, reset, firewall, traffic and cancellation-info data sources.
- **Mock-only, by choice** — `_boot_linux`/`_vnc`/`_windows` and every `*_cancellation` resource (reinstall / cancel), and **all ordering**: the three order resources, the product data sources and `_currency`. Ordering stays disabled for the test user on purpose; `test = true` is documented behaviour, never observed — the README carries a caution about it.
- **Mock-only, nothing to test on** — `_failover`, `_ip_mac`/`_subnet_mac` (`MAC_NOT_AVAILABLE` for the auction server's addresses), the Storage Box resources.

Everything else follows the documentation and runs through acceptance tests against `mock_*_test.go`, an in-memory model of the webservice; the live configurations also run against the mock (`TestAccMockLiveSuite`). When a live run contradicts the docs: teach the mock first, then fix the provider, then record the finding below and in the README.

Two layers coexist:

- **Node meta layer** (from the `scaffold` template): pnpm + oxlint + oxfmt + husky + commitlint + release-please + CI/CodeQL/Dependabot + issue/PR templates. Gates config/docs (JSON/YAML/MD).
- **Go provider**: `internal/client/` (the webservice client) and `internal/provider/` (resources, data sources, tests).

## API shape (documentation, checked live where noted)

- Base `https://robot-ws.your-server.de`, **HTTP Basic auth** with a webservice user. **Three failed logins block the calling IP for ten minutes** — the client never retries a 401.
- Requests are **form-encoded** (`key[]=a&key[]=b`, firewall rules as `rules[input][0][name]=…`), also the bodies of `DELETE /vswitch/{id}` and `DELETE /vswitch/{id}/server`. Responses wrap the entity in its name (`{"server": {…}}`, lists `[{"server": {…}}]`); **vSwitch answers are bare objects**. `client.GetObject` / `GetList` / `SendObject` unwrap.
- **An empty collection is a 404**, not `[]` — `NOT_FOUND`, or the collection's own code for top-level lists (`SERVER_NOT_FOUND` on `/server`). `GetList` maps that to an empty slice; below an entity only `NOT_FOUND` counts as empty (a missing parent stays an error).
- Errors are `{"error": {"status", "code", "message"}}` plus `missing`/`invalid` for `INVALID_INPUT`. **The rate limit is a 403 `RATE_LIMIT_EXCEEDED`** with hourly windows per endpoint — never retried. Retries: transport errors and 5xx on GET, 503 (maintenance) on writes — **except `/order/*`**, where a duplicate would buy twice.
- Types are inconsistent across the doc (`server_number` Integer vs String, a subnet `mask` 29 vs `"29"`, `cancellation_date` vs `cancellation-date`, boot `os`/`dist`/`lang` an array while inactive and a string once active). `client.FlexInt`/`FlexString`/`FlexFloat`/`StringOrList` accept every spelling.
- **Live findings (2026-10):** `/order/*` answers **401 `UNAUTHORIZED`** — not the documented 412 — while ordering is not enabled for the webservice user (whether that counts towards the IP block is unknown, so the live suite only touches `/order` on opt-in). An empty `/vswitch` is `200 []`, not a 404, and a vSwitch cancelled `now` disappears from the list at once. Reading the cancellation of an **IPv6 subnet** is a 409 `CONFLICT` ("cancellation of IPv6 subnet is not possible"); the main IPv4 address answers it normally. Responses carry undocumented extras (`vswitch_id` on subnets, `arch` and `boot_time` on boot configs) — the decoders ignore them.
- **Storage Box endpoints only work for the inclusive BX10**; every write there is limited to **1 per 5 seconds** — the client paces non-GET `/storagebox*` calls.
- Firewall and vSwitch changes are asynchronous (`in process`); the resources wait (`waitFor`, `pollInterval`). Orders wait for their transaction a minute apart (`orderPollInterval`) because transaction reads are capped at 500 per hour.

## Semantics decisions

- **Adopt, don't create, what Robot can't create.** `hetznerrobot_server`, `_ip`, `_subnet`, `_storagebox` adopt by natural key; destroying them only forgets. Cancelling is its own resource (`*_cancellation`); a cancellation whose entity is gone stays in state as the record of it.
- **Orders never order twice.** State is saved the moment Robot accepts the POST; a timeout or a `cancelled` transaction is a warning, never an error (an error would taint and re-order). Optional order arguments only replace when a set value changes to another set value (`orderReplaceOnChange`); import fails unless the transaction is still readable (30 days). Destroy forgets, with a warning.
- **Boot configurations survive their reboot.** Robot clears `active` once the server boots the config; Read only refreshes `active` and never removes or re-plans the activation.
- **Firewall POSTs replace the whole configuration.** Create reads the live firewall first so unlisted rules, flags and status are kept; `status` defaults to `active` only when rules or a template are configured.
- **Resets and Wake on LAN are one-shot resources** with `triggers` (like `terraform_data`); import sets the keys without an API call.
- `replaceOnSetChange` (helpers) is the pattern for arguments an import cannot read back: setting or unsetting them is an in-place no-op, only a real change replaces.

## Commands

Go (via `GNUmakefile`; needs **Go ≥ 1.26**). `go.mod` pins `toolchain go1.26.9`: `setup-go` in CI and the release reads that line. Raise it when a newer Go patch fixes the standard library; keep it at a version the local `golangci-lint` was built with or newer.

| Command        | What it does                                                                     |
| :------------- | :------------------------------------------------------------------------------- |
| `make build`   | `go build -o terraform-provider-hetznerrobot`                                    |
| `make tidy`    | `go mod tidy`                                                                    |
| `make fmt`     | `gofmt -s -w .`                                                                  |
| `make vet`     | `go vet ./...`                                                                   |
| `make lint`    | `golangci-lint run`                                                              |
| `make docs`    | render `docs/` from the schema (build + export + tfplugindocs)                   |
| `make test`    | `go test ./...` — client unit tests, examples coverage                           |
| `make testacc` | `TF_ACC=1 go test ./...` — mock acceptance tests; needs a TF binary, no account  |
| `make vuln`    | `govulncheck ./...` — reachable vulnerabilities, standard library included       |

**Live tests** (real webservice; throwaway entities and restored settings; `HETZNER_ROBOT_LIVE_DISRUPT` adds the firewall rewrite and the resets — only for an idle server; never a reinstall, cancellation, reroute or order):

```bash
TF_ACC=1 HETZNER_ROBOT_LIVE=1 HETZNER_ROBOT_USER=… HETZNER_ROBOT_PASSWORD=… \
  HETZNER_ROBOT_LIVE_SERVER=<n> HETZNER_ROBOT_LIVE_WRITE=1 HETZNER_ROBOT_LIVE_VSWITCH=1 \
  HETZNER_ROBOT_LIVE_DISRUPT=1 \
  go test ./internal/provider -run TestLive -v
```

Node meta layer: `pnpm install` (wires husky hooks), `pnpm check` / `pnpm check:fix`. CI (`.github/workflows/ci.yml`) runs the central **Go job** (build·vet·gofmt·golangci-lint·test + `TF_ACC` mock acceptance tests) and the **check job** (`pnpm check`).

> [!NOTE]
> Generated files are excluded from oxfmt via `.prettierignore` (`docs/` from tfplugindocs, `CHANGELOG.md` from release-please). Don't reformat them — the next generation undoes it.

## Patterns & gotchas

- One file per area (resource + its data sources), `api*` wire structs, `*Model` framework structs; constructors `New{Camel}Resource` / `New{Camel}DataSource`; TypeName `hetznerrobot_{snake}`; everything registered in `provider.go`.
- No synthetic `id` attributes: resources are keyed by their natural key (`server_number`, `ip`, `fingerprint`, `transaction_id`, …); tests pass `ImportStateVerifyIdentifierAttribute`.
- **Optional + Computed + `UseStateForUnknown`** (`optStr`/`optInt`/`optBool`) on settings; values Robot changes on its own are plain `computed*` (no UseStateForUnknown), values fixed for the entity's lifetime `stable*`.
- **The mock** (`mock_test.go`) is a route table: each area registers its endpoints and fixtures from an `init()` in its own `mock_*_test.go`. Shared fixtures (servers 321/421, IPs, subnets, failover, Storage Box) live in `mock_fixtures_test.go`. Query parameters only count for GETs, so a write that puts its fields in the URL fails as it would at Robot.
- `examples_test.go` fails when a type has no example or an example no type; `mock_coverage_test.go` fails when a type appears in no acceptance-test configuration.

### Manual smoke test (loads the binary in OpenTofu)

```bash
make build
cat > /tmp/hetznerrobot.tfrc <<EOF
provider_installation {
  dev_overrides { "kirchdev/hetznerrobot" = "$(pwd)" }
  direct {}
}
EOF
TF_CLI_CONFIG_FILE=/tmp/hetznerrobot.tfrc tofu -chdir=path/to/example plan
```

## Release & publishing

- **release-please** (`.github/workflows/release-please.yml`, `release-type: go`) owns versioning + CHANGELOG + the tag + GitHub release.
- When it cuts a release, the **GoReleaser** job in the same workflow builds the cross-platform archives, **GPG-signs** the checksums and **appends** them to the release (`release.mode: append`).
- The registry consumes the per-platform zips + `SHA256SUMS` + detached `.sig` + `manifest.json` (protocol `6.0`, from `terraform-registry-manifest.json`).
- The GPG key in `KEYS` is the **same** key the sibling providers use — already registered, nothing to rotate.

## Conventions

- **Conventional Commits enforced** via commitlint on `git commit`. Don't `--no-verify` unless explicitly asked.
- Siblings **`../terraform-provider-tcpshield`**, **`../terraform-provider-laravelforge`**, **`../terraform-provider-discord`** and **`../terraform-provider-linear`** are the reference implementations of these provider conventions — check them when unsure.

## AI & skills

- **`.claude/settings.json`** ships a baseline permission policy — see _Permission policy_ below for the rules it follows. `.claude/settings.local.json` (per-machine overrides, typically `enabledMcpjsonServers`) is gitignored.
- **`.tituskirch-skills.json`** configures the [TitusKirch skills](https://github.com/TitusKirch/skills) (commit, PR, issue, release, docs …) per repo. It is the runtime **config**, not an installer. Regenerate/reconcile it with the `tituskirch-skills-config` skill.
- **Installing the skills.** The bundle is installed via the skills.sh CLI (`pnpm dlx skills add TitusKirch/skills`), not vendored into the repo. `pnpm skills:update` refreshes project-scoped skills tracked in `skills-lock.json` (only present once a repo actually installs project skills).

## Permission policy

`.claude/settings.json` is deliberately lopsided: a **long `deny` list and a short `allow` list**. The two sides answer different questions, so they follow opposite rules.

**`deny` may be generous.** A rule for a command the repo doesn't have is a no-op, it never needs maintenance, and it is never reviewed — a too-broad block only surfaces when you actually hit it. So the list covers every stack kirchDev repos might grow into (Laravel, Prisma, Terraform/OpenTofu, AWS), not just this one. `git reflog expire` and `git gc --prune=now` are in there because they destroy the rescue path that survives a `reset --hard`.

The line to draw is **the machine or something remote, not the working copy**. Blocked: anything that wrecks the OS (`dd`, `mkfs`, `chmod -R`, `rm -rf /…`), tears down remote state or resources (`terraform destroy`, `state rm`, `aws ec2 terminate-instances`, `gh repo delete`), or throws away work with no recovery path (force-push, `reset --hard`, `stash drop`). Deliberately *not* blocked, because they are ordinary local development: `rm -rf node_modules`, `docker volume rm`, `docker compose down -v`, `docker system prune`, `php artisan tinker`, deleting a remote branch. Those prompt instead — a command that is sometimes wanted belongs in the middle state, never in `deny`.

**`allow` must stay short.** Its only return is fewer prompts — no safety is gained. Every line has to be read and understood by whoever copies this file, and an unreviewed allow list is more dangerous than none. Keep what occurs many times per session (read-only git, `ls`/`grep`/`rg`, the project's own check scripts) and let everything else ask.

**Three states, not two.** A command in `allow` runs unasked; one in `deny` is impossible and has to be typed by hand; one in **neither list prompts you** — and that middle state is the right default for almost everything. Reserve `deny` for what a mistaken "yes" could not undo. A normal `git push` is not that: it is reversible, visible and the ordinary way work ships, so it sits in `allow`.

> [!IMPORTANT]
> **Never allow a rule that runs arbitrary code.** `php artisan tinker --execute`, `pnpm exec turbo run`, `find . *` (which covers `-delete` and `-exec rm`), a raw `pnpm dlx`, or an MCP tool that executes SQL (`database-query`, `run-query`) each hand back everything the `deny` list took away — a blocked `db:wipe` means nothing next to an allowed `tinker --execute 'DB::statement(...)'`. A deny list is only as strong as the weakest allow rule beside it.

Two things this file cannot do, by design: it cannot tell which branch a `git push` targets (protect release branches with **branch protection**, not permissions), and prefix rules miss flags placed before the subcommand (`docker compose -f x.yml down -v`). Treat it as lowering the odds, not as a guarantee.

Downstream repos keep the `deny` list as-is and swap the `pnpm` lines in `allow` for whatever their stack runs.

**Codex gets the same policy** in `.codex/rules/default.rules` — permission config is not portable, so the block list exists twice and **both must be changed together**. Codex uses Starlark `prefix_rule()` calls matching on argument *tokens*, which handles flags and shell chains that the `Bash(…)` prefix patterns miss, and every rule carries its own `match`/`not_match` cases. Check a rule with:

```bash
codex execpolicy check --pretty --rules .codex/rules/default.rules -- git push --force
```

**Parity between the two is machine-checked, not eyeballed.** `pnpm check:policy` (`scripts/check-policy-parity.ts`, part of `pnpm check` and of CI) expands every `prefix_rule` into its concrete argv prefixes — the cartesian product over its alternation lists — and matches the two sets in both directions, so "we changed both files" becomes a number rather than a claim. Two things it encodes are worth knowing before editing either file:

- **The languages differ, so a few gaps cannot be closed.** Claude Code matches a prefix of the command _string_; a `prefix_rule` matches whole argv _tokens_. `Bash(aws iam delete-:*)` therefore bans every delete verb AWS will ever ship, and the Codex side can only enumerate the ones it ships today. Such a difference is legal but must be **declared** — in the `DELIBERATE` list in the script and in the `.codex/rules/default.rules` header — and the check fails both on an undeclared one and on a declaration that has gone stale.
- **Neither language normalises flag order or case.** `rm -rf /` and `rm -fr /` are separate bans; `rm -r -f /` and `redis-cli FlushAll` are neither, and enumerating permutations never ends. The check proves the two files list the **same spellings** — it does not claim the set of spellings is complete. Same caveat as the two below, and for the same reason.

## Workflows are calls, not copies

Every file in `.github/workflows/` is a **stub**: a trigger and a `uses:` pointing at a body in [`kirchDev/workflows`](https://github.com/kirchDev/workflows). A repo created from this template inherits the calls, not 727 lines of workflow — and a fix made centrally reaches it on its next Dependabot bump instead of never.

What follows for a new repo:

- **Do not paste a workflow body back in.** If a stub almost fits, the answer is an input on the body or an own job beside the call — see that repository's `docs/1.guides/2.add-a-body.md`.
- **The pins are commit SHAs with the version as a trailing comment.** Dependabot raises the bumps; the `github-actions` ecosystem is already configured in `.github/dependabot.yml`.
- **A repo that publishes something** adds its own job to `release-please.yml`, gated on `needs.release-please.outputs.release-created`.
- **A repo with a compiled language** names it in `codeql.yml`'s `languages` input rather than forking the workflow.
- **Checks come from `package.json`.** `ci.yml` runs whatever the `check` script chains, so adding a check needs no workflow change at all.

## Branching model

The default here is a **`dev` integration branch**: branch off `dev`, PR into `dev`, roll `dev` up into `main`, and release-please releases from `main`. That is what most kirchDev repos run, so the template runs it too — a variant that ships switched off is a variant nobody notices is broken.

> [!IMPORTANT]
> A repo created from this template has the `dev` config but **no `dev` branch**. Create it before the first Dependabot run: with `target-branch: 'dev'` pointing at a branch that doesn't exist, Dependabot opens nothing at all. Going main-only (below) is a deliberate step too — leaving the config untouched is the one option that silently does nothing.

`.github/workflows/promotion-pr.yml` opens and updates the rolling draft promotion PR. Mark that PR ready and **merge it with a merge commit, never a squash**: squashing collapses the individual `feat:`/`fix:` commits into the PR's own `chore:` title, and release-please then cuts nothing.

It calls a central body that picks its own target: with a `stage` branch it promotes `dev` into `stage`, without one straight into `main`. The stub is therefore the same file whichever flow a repo is on.

Going **main-only** is three edits, all of them removals:

```bash
rm .github/workflows/promotion-pr.yml
# .github/dependabot.yml    — drop both `target-branch: 'dev'` lines
# .tituskirch-skills.json   — set `pr.base` to "main"
```

Nothing is vendored for this. A variant worth shipping as files is one that *adds* something — content that would otherwise be lost. A variant that only deletes has nothing to preserve, so it stays documented.

`ci.yml` and `codeql.yml` list both `main` and `dev` in their `on: branches:` filters and neither edit touches them. A filter naming a branch that doesn't exist is a no-op, so it costs a main-only repo nothing — and without `dev` in `ci.yml`, PRs into `dev` (Dependabot's included) would run no CI at all.

## House style for READMEs and meta files

`/write-readme` skill encodes the canonical structure. Key rules: hero block wrapped in `<div align="center">`, prescribed section emojis (✨ 📦 🗺️ 📚 🤝 🛣️ 📄), license footer always reads `[MIT](LICENSE) © [Titus Kirch](https://github.com/TitusKirch/) / [IT-Dienstleistungen Titus Kirch](https://kirch.dev)`. Use GitHub callouts (`> [!TIP]`, `> [!IMPORTANT]`), never plain blockquotes.
