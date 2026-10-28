// Tests for the product-scenario proof kind (scripts/claims.mjs). Run with
// `npm run test:claims`.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { FIELDS, END, START, counts, problems, proofProblems, render, splice } from './claims.mjs';

// A row of plywood's pw-disjoint scenario for the c5-disjoint claim, as the harness will record it.
const row = {
	kind: 'product-scenario',
	repo: 'INTENTIUS/plywood',
	scenario: 'pw-disjoint',
	claim: 'c5-disjoint',
	commit: '0123abcd4567',
	date: '2026-11-02',
	engine_version: '0.1.0',
};
const claim = (proofs, pending = []) => ({ id: 'c5-disjoint', engine_version: '0.1.0', proofs, pending });
const file = (...claims) => ({ format: 1, claims });

test('accepts a proof with the six fields', () => {
	assert.deepEqual(proofProblems(row), []);
	assert.deepEqual(problems(file(claim([row]))), []);
});

for (const field of FIELDS) {
	test(`rejects a proof missing ${field}`, () => {
		const { [field]: _gone, ...rest } = row;
		assert.deepEqual(proofProblems(rest), [`${field} is missing`]);
		assert.deepEqual(problems(file(claim([rest]))), [`claims[0] (c5-disjoint) proofs[0]: ${field} is missing`]);
	});
}

test('rejects an empty field, a field of the wrong shape and another kind', () => {
	assert.deepEqual(proofProblems({ ...row, scenario: '' }), ['scenario must be a non-empty string']);
	assert.match(proofProblems({ ...row, date: '2/11/2026' })[0], /date "2\/11\/2026" is not YYYY-MM-DD/);
	assert.match(proofProblems({ ...row, engine_version: 'v1' })[0], /engine_version/);
	assert.deepEqual(proofProblems({ ...row, kind: 'go_test' }), ['kind must be "product-scenario"']);
});

test('rejects a proof listed under another claim', () => {
	assert.match(problems(file(claim([{ ...row, claim: 'c3' }])))[0], /not the claim it is listed under/);
});

test('rejects a file of another format', () => {
	assert.equal(problems({ claims: [] }).length, 1);
});

test('a row counts only at the version the claim is about', () => {
	const other = { ...row, engine_version: '0.0.9' };
	assert.equal(counts(row, claim([row])), true);
	assert.equal(counts(other, claim([other])), false);
});

test('renders a row with a link, and a row at another version as not counted', () => {
	const other = { ...row, engine_version: '0.0.9', scenario: 'pw-disjoint-old' };
	const out = render(file(claim([row, other])));
	assert.ok(out.includes('| `c5-disjoint` | INTENTIUS/plywood | [pw-disjoint](https://github.com/INTENTIUS/plywood/commit/0123abcd4567) | `0123abcd4567` | 2026-11-02 | 0.1.0 | yes |'));
	assert.ok(out.includes('| 0.0.9 | no, the claim is about 0.1.0 |'));
});

test('lists a pending citation as pending, not as a row', () => {
	const out = render(file(claim([], [{ repo: 'INTENTIUS/plywood', scenario: 'pw-disjoint', note: 'markers' }])));
	assert.ok(out.includes('No row is recorded yet.'));
	assert.ok(out.includes('| `c5-disjoint` | INTENTIUS/plywood | `pw-disjoint` | markers |'));
	assert.ok(!out.includes('| Engine version |'));
});

test('splice replaces only the block and refuses a page without markers', () => {
	const page = `before\n${START}\nold\n${END}\nafter\n`;
	assert.equal(splice(page, 'NEW'), 'before\nNEW\nafter\n');
	assert.throws(() => splice('no markers', 'NEW'), /no product-scenario block/);
});

test('the committed claims.json is valid, lists the three plywood scenarios as pending, and claims.md is what it renders to', () => {
	const read = (rel) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');
	const doc = JSON.parse(read('../src/data/claims.json'));
	assert.deepEqual(problems(doc), []);
	const pending = doc.claims.flatMap((c) => c.pending.map((p) => `${c.id}:${p.scenario}`));
	for (const want of ['c5-disjoint:pw-disjoint', 'c3:pw-moved-plan', 'adopt:pw-adopt']) assert.ok(pending.includes(want), want);
	const page = read('../src/content/docs/claims.md');
	assert.equal(splice(page, render(doc)), page);
});
