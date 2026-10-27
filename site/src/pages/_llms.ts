// The agent index, built from the docs collection so it never drifts from the
// site. llms.txt lists every page; llms-full.txt carries their text.
import { getCollection } from 'astro:content';
import { bodyMarkdown, pageUrl } from '../markdown';
import { neverLine } from '../prompts';

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
			// Every prompt ends with neverLine; PREAMBLE says it once instead.
			prompt: d.data.prompt?.replace(neverLine, '').trim(),
			body: bodyMarkdown(d.body ?? ''),
		}));
}

export const PREAMBLE = `# plunge

> Pulumi with resource level concurrency and no stack lock

The source, and the reference material the site links to, is at https://github.com/INTENTIUS/plunge.

https://intentius.io/plunge/claims/ lists the recorded runs with their dates and files.

A page's prompt sits under "Optional: hand this page to your coding agent" and in llms.txt under the page. Every prompt ends with this rule, left out below: ${neverLine}`;
