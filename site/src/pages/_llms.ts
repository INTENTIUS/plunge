// The agent index, built from the docs collection so it never drifts from the
// site. llms.txt lists every page; llms-full.txt carries their text.
import { getCollection } from 'astro:content';
import { bodyMarkdown, pageUrl } from '../markdown';

// Sidebar order. A page not listed here comes after these, by id.
const ORDER = ['use/start', 'how-it-works', 'claims'];

export async function pages() {
	const docs = await getCollection('docs');
	const rank = (id: string) => {
		const i = ORDER.indexOf(id);
		return i === -1 ? ORDER.length : i;
	};
		return docs
		.sort((a, b) => rank(a.id) - rank(b.id) || a.id.localeCompare(b.id))
		.map((d) => ({
			id: d.id,
			title: d.data.title,
			description: d.data.description ?? '',
			url: pageUrl(d.id),
			prompt: d.data.prompt,
			body: bodyMarkdown(d.body ?? ''),
		}));
}

export const PREAMBLE = `# plunge

> plunge is Pulumi that tags each resource with the stack that owns it and keeps one record per resource, with no stack lock.

The source, and the reference material the site links to, is at https://github.com/INTENTIUS/plunge.

A claim on this site is backed by a recorded run, listed with its date at https://intentius.io/plunge/claims/. Anything built but not yet run links to its GitHub issue.

A page with a prompt carries it under "Optional: hand this page to your coding agent", and llms.txt lists it under the page. Every prompt forbids \`plunge up\`, \`plunge destroy\`, \`plunge stack adopt --yes\` and merges; those stay with the person.`;
