#!/usr/bin/env node

/**
 * Proves the two agent policy files ban the same commands.
 *
 * `.claude/settings.json` (Claude Code) and `.codex/rules/default.rules`
 * (Codex) carry the same block list in two languages, and nothing but this
 * check stops them drifting apart — the same destructive command refused
 * under one tool and merely prompted under the other.
 *
 * The comparison is mechanical, never by inspection: every `prefix_rule`
 * block is expanded into its concrete argv prefixes (the cartesian product
 * over its alternation lists), and the two sets are matched in both
 * directions.
 *
 * Matching follows each engine's own semantics, which are not the same:
 *
 *   - Claude Code matches a prefix of the command *string*, so
 *     `Bash(aws iam delete-:*)` bans `aws iam delete-role` mid-token. An
 *     entry without the `:*` suffix matches that one command exactly.
 *   - A `prefix_rule` matches whole argv *tokens*, so it covers a command
 *     only when its tokens are a leading run of the command's tokens.
 *
 * That asymmetry is why a Claude entry ending mid-token can never be closed
 * by enumeration. Such an entry is reported as a structural difference and
 * has to be listed in DELIBERATE below, with its reason. Anything else is a
 * gap, and a gap fails the check.
 */

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const CLAUDE_FILE = '.claude/settings.json';
const CODEX_FILE = '.codex/rules/default.rules';

type Deliberate = {
  deny: string;
  reason: string;
};

/**
 * Claude `deny` entries that no `prefix_rule` can express, each with the
 * reason it stays open. An entry here still has to be a *structural*
 * difference — one the Codex side extends but cannot close. A stale entry
 * fails the check rather than lingering as a false claim.
 */
const DELIBERATE: Deliberate[] = [
  {
    deny: 'Bash(aws iam delete-:*)',
    reason:
      'Bans every "aws iam delete-…" verb, present and future, by matching ' +
      'mid-token. A prefix_rule matches whole argv tokens and has no ' +
      'open-ended form, so the Codex rule enumerates the verbs AWS ships ' +
      'today. That enumeration is a snapshot; closing the rest is a limit ' +
      'of the rule language, not a decision.'
  }
];

/** A parsed Claude `Bash(...)` deny entry. */
type ClaudeEntry = {
  /** The entry exactly as written in the deny list. */
  raw: string;
  /** The command prefix, with any trailing `:*` removed. */
  prefix: string;
  /** Whether the entry ended in `:*` and so matches open-endedly. */
  open: boolean;
  /** The prefix split into argv tokens. */
  tokens: string[];
};

/** One concrete argv prefix expanded out of a `prefix_rule` pattern. */
type CodexPrefix = {
  tokens: string[];
  command: string;
};

/** A forbidding `prefix_rule` block and the prefixes it expands to. */
type CodexRule = {
  line: number;
  prefixes: CodexPrefix[];
};

/** A `prefix_rule` pattern slot: one fixed token, or a set of alternatives. */
type PatternSlot = string | string[];

function read(file: string): string {
  return readFileSync(join(root, file), 'utf8');
}

/** Slice the balanced `open`…`close` run starting at `start`. */
function sliceBalanced(
  text: string,
  start: number,
  open: string,
  close: string
): string | null {
  let depth = 0;
  for (let i = start; i < text.length; i += 1) {
    if (text[i] === open) {
      depth += 1;
    } else if (text[i] === close) {
      depth -= 1;
      if (depth === 0) {
        return text.slice(start, i + 1);
      }
    }
  }
  return null;
}

function lineOf(text: string, index: number): number {
  return text.slice(0, index).split('\n').length;
}

/** Every `Bash(...)` entry in the deny list, as a prefix plus its tokens. */
function parseClaudeDeny(text: string): {
  entries: ClaudeEntry[];
  unparsed: string[];
} {
  const parsed = JSON.parse(text) as {
    permissions?: { deny?: string[] };
  };
  const denied = parsed.permissions?.deny ?? [];
  const entries: ClaudeEntry[] = [];
  const unparsed: string[] = [];

  for (const raw of denied) {
    const match = /^Bash\((?<spec>.*)\)$/s.exec(raw);
    if (!match) {
      unparsed.push(raw);
      continue;
    }

    const spec = match.groups!.spec!;
    const open = spec.endsWith(':*');
    const prefix = open ? spec.slice(0, -2) : spec;
    entries.push({ raw, prefix, open, tokens: prefix.split(' ') });
  }

  return { entries, unparsed };
}

/** The cartesian product of a pattern's slots, each an argv token run. */
function expand(pattern: PatternSlot[]): string[][] {
  let rows: string[][] = [[]];
  for (const slot of pattern) {
    const options = Array.isArray(slot) ? slot : [slot];
    rows = rows.flatMap((row) => options.map((token) => [...row, token]));
  }
  return rows;
}

/** Every forbidding `prefix_rule`, expanded into concrete argv prefixes. */
function parseCodexRules(text: string): CodexRule[] {
  const rules: CodexRule[] = [];
  const finder = /prefix_rule\s*\(/g;
  let found: RegExpExecArray | null;

  while ((found = finder.exec(text)) !== null) {
    const parenAt = found.index + found[0].length - 1;
    const block = sliceBalanced(text, parenAt, '(', ')');
    if (block === null) {
      throw new Error(
        `${CODEX_FILE}:${lineOf(text, found.index)} — unbalanced prefix_rule()`
      );
    }

    if (!/decision\s*=\s*"forbidden"/.test(block)) {
      continue;
    }

    const head = /pattern\s*=\s*/.exec(block);
    if (!head) {
      throw new Error(
        `${CODEX_FILE}:${lineOf(text, found.index)} — rule has no pattern`
      );
    }

    const literal = sliceBalanced(block, head.index + head[0].length, '[', ']');
    if (literal === null) {
      throw new Error(
        `${CODEX_FILE}:${lineOf(text, found.index)} — unbalanced pattern`
      );
    }

    const pattern = JSON.parse(
      literal.replaceAll(/,(?=\s*[\]}])/g, '')
    ) as PatternSlot[];
    rules.push({
      line: lineOf(text, found.index),
      prefixes: expand(pattern).map((tokens) => ({
        tokens,
        command: tokens.join(' ')
      }))
    });
  }

  return rules;
}

/** Does a Claude entry ban this whole command string? */
function claudeCovers(entry: ClaudeEntry, command: string): boolean {
  return entry.open
    ? command.startsWith(entry.prefix)
    : command === entry.prefix;
}

/** Are `tokens` a leading run of `of`? */
function isTokenPrefix(tokens: string[], of: string[]): boolean {
  return (
    tokens.length <= of.length && tokens.every((token, i) => token === of[i])
  );
}

const claudeText = read(CLAUDE_FILE);
const codexText = read(CODEX_FILE);
const { entries: denies, unparsed } = parseClaudeDeny(claudeText);
const rules = parseCodexRules(codexText);
const prefixes = rules.flatMap((rule) =>
  rule.prefixes.map((prefix) => ({ ...prefix, line: rule.line }))
);

// Codex forbids it — does Claude?
const missingFromClaude = prefixes.filter(
  (prefix) => !denies.some((entry) => claudeCovers(entry, prefix.command))
);

// Claude denies it — does Codex? A miss the Codex side merely *extends* is
// structural: enumeration cannot close a Claude entry that ends mid-token.
const missingFromCodex: ClaudeEntry[] = [];
const structural: { entry: ClaudeEntry; extending: CodexPrefix[] }[] = [];

for (const entry of denies) {
  if (prefixes.some((prefix) => isTokenPrefix(prefix.tokens, entry.tokens))) {
    continue;
  }

  const last = entry.tokens.length - 1;
  const extending = prefixes.filter(
    (prefix) =>
      prefix.tokens.length > last &&
      isTokenPrefix(entry.tokens.slice(0, last), prefix.tokens) &&
      prefix.tokens[last].startsWith(entry.tokens[last])
  );

  if (extending.length > 0) {
    structural.push({ entry, extending });
  } else {
    missingFromCodex.push(entry);
  }
}

const declared = new Map(DELIBERATE.map((item) => [item.deny, item]));
const undeclared = structural.filter((item) => !declared.has(item.entry.raw));
const stale = DELIBERATE.filter(
  (item) => !structural.some((found) => found.entry.raw === item.deny)
);

const problems: string[] = [];

console.log(
  `${CLAUDE_FILE}: ${denies.length} deny entries\n` +
    `${CODEX_FILE}: ${rules.length} forbidding prefix_rule blocks, ` +
    `${prefixes.length} argv prefixes\n`
);

if (unparsed.length > 0) {
  problems.push(
    `${unparsed.length} deny entries are not Bash(...) and were not compared:\n` +
      unparsed.map((raw) => `  ${raw}`).join('\n')
  );
}

if (missingFromClaude.length > 0) {
  problems.push(
    `Forbidden by Codex, only prompted under Claude — ${missingFromClaude.length}:\n` +
      missingFromClaude
        .map((p) => `  ${p.command}  (${CODEX_FILE}:${p.line})`)
        .join('\n')
  );
}

if (missingFromCodex.length > 0) {
  problems.push(
    `Denied by Claude, only prompted under Codex — ${missingFromCodex.length}:\n` +
      missingFromCodex.map((entry) => `  ${entry.raw}`).join('\n')
  );
}

if (undeclared.length > 0) {
  problems.push(
    `Structural differences not declared in DELIBERATE — ${undeclared.length}:\n` +
      undeclared
        .map(
          (item) =>
            `  ${item.entry.raw} — Codex extends it with ` +
            `${item.extending.length} enumerated commands but cannot match ` +
            `the open prefix itself. Close it, or record it in DELIBERATE ` +
            `in this script and in the ${CODEX_FILE} header.`
        )
        .join('\n')
  );
}

if (stale.length > 0) {
  problems.push(
    `DELIBERATE entries that are no longer a structural difference — ${stale.length}:\n` +
      stale
        .map(
          (item) =>
            `  ${item.deny} — Codex now covers it (or the deny entry is ` +
            `gone). Remove it from DELIBERATE and from the ${CODEX_FILE} header.`
        )
        .join('\n')
  );
}

if (problems.length > 0) {
  console.error(`${problems.join('\n\n')}\n`);
  console.error('Policy parity check failed.');
  process.exitCode = 1;
} else {
  if (structural.length > 0) {
    console.log(`Deliberate structural differences — ${structural.length}:`);
    for (const item of structural) {
      console.log(`  ${item.entry.raw}`);
      console.log(`    ${declared.get(item.entry.raw)!.reason}`);
      console.log(
        `    Codex enumerates ${item.extending.length}: ` +
          `${item.extending.map((p) => p.tokens.at(-1)).join(', ')}`
      );
    }
    console.log('');
  }

  console.log('Policy parity check passed: both files ban the same commands.');
}
