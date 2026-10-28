// The claims source for src/content/docs/claims.md (INTENTIUS/plunge#57,
// chant#4586 ruling C).
//
// src/data/claims.json lists the claims that cite a product's scenario row:
//
//   { "format": 1,
//     "claims": [
//       { "id": "c5-disjoint",
//         "engine_version": "0.1.0",          the plunge version the claim is about
//         "proofs": [
//           { "kind": "product-scenario",
//             "repo": "INTENTIUS/plywood",    the product that ran the scenario
//             "scenario": "pw-disjoint",
//             "claim": "c5-disjoint",         the claim id the row records; the claim's own id
//             "commit": "0123abc",            the product commit the row ran at
//             "date": "2026-11-02",
//             "engine_version": "0.1.0" }     the plunge version the row ran
//         ],
//         "pending": [ { "repo": "INTENTIUS/plywood", "scenario": "pw-disjoint", "note": "what it will record" } ] } ] }
//
// The six fields are the ones choudoufu's claims.json and sql-yodeler's
// product-proofs.json use for the same kind. A row counts only when its
// engine_version is the claim's. A row at another version is shown and marked
// not counted. A pending citation names a scenario whose row is not recorded
// yet; it shows as pending and never as a run. A product row never replaces
// the engine's own runs, which stay in the table above.
//
//   node scripts/claims.mjs --check    fail when claims.json is wrong or claims.md is not what it renders to
//   node scripts/claims.mjs --write    rewrite the block of claims.md between the markers
//
// The block is the text between START and END below.

import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const KIND = 'product-scenario';
export const FIELDS = ['repo', 'scenario', 'claim', 'commit', 'date', 'engine_version'];
export const START = '<!-- product-scenario rows: written by `npm run claims:render` from src/data/claims.json -->';
export const END = '<!-- end product-scenario rows -->';

const SHAPES = {
	repo: [/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/, 'owner/name'],
	scenario: [/^[a-z0-9][a-z0-9-]*$/, 'a scenario id such as pw-disjoint'],
	claim: [/^[a-z0-9][a-z0-9-]*$/, 'a claim id'],
	commit: [/^[0-9a-f]{7,40}$/, 'a git sha of 7 to 40 hex digits'],
	date: [/^\d{4}-\d{2}-\d{2}$/, 'YYYY-MM-DD'],
	engine_version: [/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/, 'a version such as 0.1.0'],
};

/** What is wrong with one proof: a missing field, a field of the wrong shape, a wrong kind. */
export function proofProblems(proof) {
	if (proof === null || typeof proof !== 'object' || Array.isArray(proof)) return ['a proof must be an object'];
	const out = [];
	if (proof.kind !== KIND) out.push(`kind must be "${KIND}"`);
	for (const f of FIELDS) {
		const v = proof[f];
		if (v === undefined) out.push(`${f} is missing`);
		else if (typeof v !== 'string' || v === '') out.push(`${f} must be a non-empty string`);
		else if (!SHAPES[f][0].test(v)) out.push(`${f} ${JSON.stringify(v)} is not ${SHAPES[f][1]}`);
	}
	return out;
}

/** Every problem with a parsed claims.json, each naming where it is. */
export function problems(doc) {
	if (doc === null || typeof doc !== 'object' || doc.format !== 1 || !Array.isArray(doc.claims)) {
		return ['claims.json: expected { "format": 1, "claims": [...] }'];
	}
	const out = [];
	const ids = new Set();
	doc.claims.forEach((c, i) => {
		const at = `claims[${i}]`;
		if (typeof c?.id !== 'string' || !SHAPES.claim[0].test(c.id)) return out.push(`${at}: id must be a claim id`);
		if (ids.has(c.id)) out.push(`${at}: claim ${c.id} is listed twice`);
		ids.add(c.id);
		if (typeof c.engine_version !== 'string' || !SHAPES.engine_version[0].test(c.engine_version)) out.push(`${at} (${c.id}): engine_version must be the version the claim is about, such as 0.1.0`);
		if (!Array.isArray(c.proofs)) out.push(`${at} (${c.id}): proofs must be a list`);
		(c.proofs ?? []).forEach((p, j) => {
			for (const why of proofProblems(p)) out.push(`${at} (${c.id}) proofs[${j}]: ${why}`);
			if (p?.claim !== undefined && p.claim !== c.id) out.push(`${at} (${c.id}) proofs[${j}]: claim ${JSON.stringify(p.claim)} is not the claim it is listed under`);
		});
		(c.pending ?? []).forEach((p, j) => {
			for (const f of ['repo', 'scenario']) {
				if (typeof p?.[f] !== 'string' || !SHAPES[f][0].test(p[f])) out.push(`${at} (${c.id}) pending[${j}]: ${f} must be ${SHAPES[f][1]}`);
			}
		});
	});
	return out;
}

/** A proof counts when the row ran the version the claim is about. */
export const counts = (proof, claim) => proof.engine_version === claim.engine_version;

const link = (p) => `https://github.com/${p.repo}/commit/${p.commit}`;

/** The block between the markers, for a parsed claims.json. */
export function render(doc) {
	const out = [
		START,
		'',
		'## Product scenario rows',
		'',
		"A claim can cite a row of a product's scenario. The table below gives each cited row its repo and scenario, the product commit and date, and the plunge version it ran. It counts only at the version the claim is about. At any other version it is listed and marked not counted. It never replaces the runs in the table above.",
		'',
	];
	const rows = doc.claims.flatMap((c) => (c.proofs ?? []).map((proof) => ({ claim: c, proof })));
	if (rows.length) {
		out.push('| Claim | Repo | Scenario | Commit | Date | Engine version | Counts |', '|---|---|---|---|---|---|---|');
		for (const { claim: c, proof: p } of rows) {
			out.push(`| \`${c.id}\` | ${p.repo} | [${p.scenario}](${link(p)}) | \`${p.commit}\` | ${p.date} | ${p.engine_version} | ${counts(p, c) ? 'yes' : `no, the claim is about ${c.engine_version}`} |`);
		}
	} else {
		out.push('No row is recorded yet.');
	}
	const pending = doc.claims.flatMap((c) => (c.pending ?? []).map((p) => ({ claim: c, pending: p })));
	if (pending.length) {
		out.push('', 'Pending citations have no recorded run until the scenario has a row.', '', '| Claim | Repo | Scenario | To record |', '|---|---|---|---|');
		for (const { claim: c, pending: p } of pending) out.push(`| \`${c.id}\` | ${p.repo} | \`${p.scenario}\` | ${p.note ?? ''} |`);
	}
	out.push('', END);
	return out.join('\n');
}

/** `page` with the block between the markers replaced by `block`; throws when the markers are missing. */
export function splice(page, block) {
	const a = page.indexOf(START);
	const b = page.indexOf(END);
	if (a === -1 || b === -1 || b < a) throw new Error('claims.md has no product-scenario block between its markers');
	return page.slice(0, a) + block + page.slice(b + END.length);
}

const here = dirname(fileURLToPath(import.meta.url));
const dataFile = join(here, '..', 'src', 'data', 'claims.json');
const pageFile = join(here, '..', 'src', 'content', 'docs', 'claims.md');

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
	const mode = process.argv[2];
	if (mode !== '--check' && mode !== '--write') {
		console.error('usage: node scripts/claims.mjs --check | --write');
		process.exit(2);
	}
	const doc = JSON.parse(readFileSync(dataFile, 'utf8'));
	const bad = problems(doc);
	if (bad.length) {
		console.error(`src/data/claims.json:\n  ${bad.join('\n  ')}`);
		process.exit(1);
	}
	const page = readFileSync(pageFile, 'utf8');
	const want = splice(page, render(doc));
	if (mode === '--write') {
		writeFileSync(pageFile, want);
		console.log('claims.md written');
	} else if (want !== page) {
		console.error('claims.md does not match src/data/claims.json; run `npm run claims:render` and commit the page');
		process.exit(1);
	}
}
