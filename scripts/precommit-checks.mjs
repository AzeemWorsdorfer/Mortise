#!/usr/bin/env node
// Mortise pre-commit repo-wide checks.
//
// These run after lint-staged; they operate on the staging area as a whole.
// Each check exits non-zero (with a clear message) on failure.
//
// Checks:
//   1. Secret-pattern scan (sk-, api_key, BEGIN blocks, GitHub tokens).
//   2. Merge-conflict marker scan.
//   3. File-size scan (>1MB rejected).
//   4. Proto regeneration — fails if generated stubs are stale.

import { execSync } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';

// Make sure Go-installed proto plugins are reachable.
// The pre-commit hook already sets this, but setting it again makes the script
// safe to run directly (`node scripts/precommit-checks.mjs`).
const GOBIN = process.env.GOBIN || `${process.env.HOME}/go/bin`;
process.env.PATH = `${GOBIN}:${process.env.PATH}`;

const MAX_FILE_BYTES = 1024 * 1024; // 1 MB

const SECRET_PATTERNS = [
  {
    name: 'OpenAI / Anthropic key prefix',
    pattern: /\bsk-[A-Za-z0-9_-]{20,}\b/,
  },
  {
    name: 'OpenAI project key',
    pattern: /\bsk-proj-[A-Za-z0-9_-]{20,}\b/,
  },
  {
    name: 'Generic api_key assignment',
    pattern: /\bapi[_-]?key\s*[:=]\s*['"][A-Za-z0-9_./+=-]{16,}['"]/i,
  },
  {
    name: 'PEM private key',
    pattern: /-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----/,
  },
  {
    name: 'GitHub personal access token',
    pattern: /\bghp_[A-Za-z0-9]{30,}\b/,
  },
  {
    name: 'GitHub OAuth token',
    pattern: /\bgho_[A-Za-z0-9]{30,}\b/,
  },
  {
    name: 'GitHub user-token',
    pattern: /\bghu_[A-Za-z0-9]{30,}\b/,
  },
  {
    name: 'GitHub server-token',
    pattern: /\bghs_[A-Za-z0-9]{30,}\b/,
  },
  {
    name: 'GitHub refresh-token',
    pattern: /\bghr_[A-Za-z0-9]{30,}\b/,
  },
];

const CONFLICT_MARKERS = [/^<<<<<<< /m, /^=======$/m, /^>>>>>>> /m];

// Files that are allowed to contain secret-looking patterns.
const SECRET_ALLOWLIST = new Set([
  '.env.example',
  'docs/security/threat-model.md',
]);

// Slash-delimited path prefixes that are allowed to contain secrets.
const SECRET_ALLOWLIST_PREFIXES = ['docs/security/'];

/** Read staged files from git. Returns: [{ path, status }] where status is A/M/D/etc. */
function getStagedFiles() {
  const raw = execSync('git diff --cached --name-status --diff-filter=ACMRT', {
    encoding: 'utf8',
  });
  return raw
    .split('\n')
    .filter(Boolean)
    .map((line) => {
      const [status, ...rest] = line.split('\t');
      return { status, path: rest.join('\t') };
    });
}

function isAllowlisted(path) {
  if (SECRET_ALLOWLIST.has(path)) return true;
  return SECRET_ALLOWLIST_PREFIXES.some((p) => path.startsWith(p));
}

/**
 * Walk staged files, reading each as UTF-8 text.
 * Calls `visitor(path, content)` for each readable file.
 * Binary/unreadable files are silently skipped.
 */
function walkStagedTextFiles(files, visitor) {
  for (const { path } of files) {
    let content;
    try {
      content = readFileSync(path, 'utf8');
    } catch {
      // Binary file or unreadable — skip.
      continue;
    }
    visitor(path, content);
  }
}

function checkSecrets(files) {
  const failures = [];
  walkStagedTextFiles(files, (path, content) => {
    if (isAllowlisted(path)) return;
    for (const { name, pattern } of SECRET_PATTERNS) {
      const match = content.match(pattern);
      if (match) {
        failures.push({ path, name, sample: match[0].slice(0, 12) + '…' });
      }
    }
  });
  return failures;
}

function checkConflictMarkers(files) {
  const failures = [];
  walkStagedTextFiles(files, (path, content) => {
    for (const marker of CONFLICT_MARKERS) {
      if (marker.test(content)) {
        failures.push({ path, marker: marker.toString() });
        break;
      }
    }
  });
  return failures;
}

function checkFileSize(files) {
  const failures = [];
  for (const { path } of files) {
    let stat;
    try {
      stat = statSync(path);
    } catch {
      continue;
    }
    if (stat.size > MAX_FILE_BYTES) {
      failures.push({
        path,
        sizeBytes: stat.size,
        sizeMB: (stat.size / 1024 / 1024).toFixed(2),
      });
    }
  }
  return failures;
}

function checkProtoFresh() {
  // Run buf generate, then check that daemon/gen and tui/src/gen are clean.
  // We do NOT amend the commit — we just block it and let the user re-run.
  try {
    execSync('npx buf generate', { stdio: 'pipe' });
  } catch (err) {
    return { ok: false, error: 'buf generate failed: ' + err.message };
  }
  // Workaround for protoc-gen-connect-es bug: it generates `./agent_pbjs`
  // (missing dot) instead of `./agent_pb.js`. We apply the fix so the diff
  // doesn't flag a spurious mismatch.
  try {
    execSync(
      "sed -i '' 's|from \"./agent_pbjs\"|from \"./agent_pb.js\"|g' tui/src/gen/mortise/v1/agent_connect.ts",
      { stdio: 'pipe' },
    );
  } catch (err) {
    return { ok: false, error: 'import fix failed: ' + err.message };
  }
  try {
    execSync('git diff --exit-code -- daemon/gen tui/src/gen', { stdio: 'pipe' });
    return { ok: true };
  } catch {
    return {
      ok: false,
      error: 'proto stubs are stale. Run `make proto` and re-stage the generated files.',
    };
  }
}

function main() {
  const files = getStagedFiles();
  if (files.length === 0) {
    console.log('No staged files — skipping.');
    return;
  }

  let failed = false;

  const secrets = checkSecrets(files);
  if (secrets.length > 0) {
    failed = true;
    console.error('\n[secret-scan] ✗ secret-like patterns detected:');
    for (const f of secrets) {
      console.error(`  ${f.path}: ${f.name} (${f.sample})`);
    }
    console.error(
      '  → If this is a false positive, move the file into docs/security/ or .env.example.',
    );
  } else {
    console.log('[secret-scan] ✓');
  }

  const conflicts = checkConflictMarkers(files);
  if (conflicts.length > 0) {
    failed = true;
    console.error('\n[conflict-markers] ✗ merge conflict markers detected:');
    for (const f of conflicts) {
      console.error(`  ${f.path}: ${f.marker}`);
    }
  } else {
    console.log('[conflict-markers] ✓');
  }

  const oversized = checkFileSize(files);
  if (oversized.length > 0) {
    failed = true;
    console.error('\n[file-size] ✗ files > 1 MB:');
    for (const f of oversized) {
      console.error(`  ${f.path}: ${f.sizeMB} MB`);
    }
  } else {
    console.log('[file-size] ✓');
  }

  console.log('[proto-regen] running buf generate…');
  const proto = checkProtoFresh();
  if (!proto.ok) {
    failed = true;
    console.error('\n[proto-regen] ✗', proto.error);
  } else {
    console.log('[proto-regen] ✓');
  }

  if (failed) {
    console.error('\nPre-commit checks failed. Fix the issues above and try again.');
    process.exit(1);
  }
}

main();
