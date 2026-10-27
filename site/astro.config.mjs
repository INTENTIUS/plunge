// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// expressive-code renders each code line as its own block element with no
// newline between them, so text extracted from the page (an agent, a reader
// mode, a copy of the selection) runs lines together. A newline text node as the
// last child of each line fixes that. The line is a grid, which does not render a
// whitespace-only text child, so nothing changes on screen. Copied from
// choudoufu's site/astro.config.mjs.
const newlinesBetweenLines = {
	name: 'newlines-between-lines',
	hooks: {
		postprocessRenderedBlock: ({ renderData }) => {
			const visit = (node) => {
				if (!node.children) return;
				const lines = node.children.filter((c) => c.type === 'element' && (c.properties?.className ?? []).includes('ec-line'));
				for (const line of lines.slice(0, -1)) {
					// A blank line already carries its own newline.
					const code = line.children.find((c) => c.type === 'element');
					if (code && !code.children.some((c) => c.type === 'element')) continue;
					line.children.push({ type: 'text', value: '\n' });
				}
				node.children.forEach(visit);
			};
			visit(renderData.blockAst);
		},
	},
};

// Published under the org domain at /plunge, as choudoufu's site is at
// /choudoufu (INTENTIUS/plunge#44). `base` has to match or every internal link
// 404s on Pages.
export default defineConfig({
	site: 'https://intentius.io',
	base: '/plunge',
	integrations: [
		starlight({
			title: 'plunge',
			// src/pages/404.astro is the 404 page.
			disable404Route: true,
			// The diver's image files live in public/ and are referenced by URL, not
			// imported. SiteTitle.astro draws the logo and the wordmark.
			favicon: '/favicon.ico',
			head: [
				{ tag: 'link', attrs: { rel: 'icon', type: 'image/png', href: '/plunge/favicon-32.png', sizes: '32x32' } },
				{ tag: 'link', attrs: { rel: 'apple-touch-icon', href: '/plunge/apple-touch-icon.png' } },
				{ tag: 'meta', attrs: { property: 'og:image', content: 'https://intentius.io/plunge/plunge-share.png' } },
				{ tag: 'meta', attrs: { property: 'og:image:width', content: '400' } },
				{ tag: 'meta', attrs: { property: 'og:image:height', content: '400' } },
				{ tag: 'meta', attrs: { property: 'og:image:alt', content: 'The plunge diver: a pixel-art cliff diver over the sea' } },
				{ tag: 'meta', attrs: { name: 'twitter:card', content: 'summary' } },
				{ tag: 'meta', attrs: { name: 'twitter:image', content: 'https://intentius.io/plunge/plunge-share.png' } },
			],
			expressiveCode: { plugins: [newlinesBetweenLines] },
			customCss: ['./src/styles/plunge.css'],
			// The title gets a "Copy page as Markdown" button, an llms.txt pointer and
			// the page's agent prompt. The footer adds a small diver. The head adds
			// PostHog page analytics when the build has PUBLIC_POSTHOG_KEY.
			components: {
				Head: './src/components/Head.astro',
				PageTitle: './src/components/PageTitle.astro',
				Footer: './src/components/Footer.astro',
				SocialIcons: './src/components/SocialIcons.astro',
				SiteTitle: './src/components/SiteTitle.astro',
				ThemeProvider: './src/components/ThemeProvider.astro',
				ThemeSelect: './src/components/ThemeSelect.astro',
			},
			description: 'Pulumi with resource level concurrency and no stack lock',
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/INTENTIUS/plunge' }],
			editLink: {
				baseUrl: 'https://github.com/INTENTIUS/plunge/edit/main/site/',
			},
			// The groups follow choudoufu's site: Start here, the four platforms,
			// How it works, Use, Reference. Every group but Start here starts collapsed.
			sidebar: [
				// The room pages under /for/ (src/pages/for/[id].astro), in the order of
				// src/data/rooms.ts: the stack you run today, where it runs, then the roles.
				// scripts/check-rooms.mjs fails the build when this list and rooms.ts disagree.
				{
					label: 'Start here',
					items: [
						{ label: 'From a Pulumi stack', link: '/for/pulumi-stack/' },
						{ label: 'From Pulumi Cloud', link: '/for/pulumi-cloud/' },
						{ label: 'From chant', link: '/for/chant/' },
						{ label: 'Starting new', link: '/for/new/' },
						{ label: 'On AWS', link: '/for/aws/' },
						{ label: 'On Kubernetes', link: '/for/kubernetes/' },
						{ label: 'On GCP', link: '/for/gcp/' },
						{ label: 'On Azure', link: '/for/azure/' },
						{ label: 'Security and access', link: '/for/security/' },
						{ label: 'Evaluating', link: '/for/evaluate/' },
						{ label: 'With a coding agent', link: '/for/agents/' },
						{ label: 'Contributing', link: '/for/contribute/' },
					],
				},
				...['AWS', 'Kubernetes', 'GCP', 'Azure'].map((label) => {
					const p = label.toLowerCase();
					return {
						label,
						collapsed: true,
						items: [
							{ label: 'Overview', slug: p },
							{ label: 'Adopt', slug: `${p}/adopt` },
							{ label: 'Gate', slug: `${p}/gate` },
							{ label: 'Operate', slug: `${p}/operate` },
							{ label: 'Compatibility', slug: `${p}/compatibility` },
							{ label: 'Proof', slug: `${p}/proof` },
						],
					};
				}),
				{
					label: 'How it works',
					collapsed: true,
					items: [
						{ label: 'Overview', slug: 'how-it-works' },
						{ label: 'Identity', slug: 'how-it-works/identity' },
						{ label: 'Records', slug: 'how-it-works/records' },
						{ label: 'Names', slug: 'how-it-works/names' },
						{ label: 'Resource-level concurrency', slug: 'how-it-works/concurrency' },
						{ label: 'Deletes', slug: 'how-it-works/deletes' },
						{ label: 'Drift of one resource', slug: 'how-it-works/drift' },
					],
				},
				{
					label: 'Use',
					collapsed: true,
					items: [
						{ label: 'Tutorial', slug: 'use/tutorial' },
						{ label: 'Start a new stack', slug: 'use/start' },
						{ label: 'Set up by hand', slug: 'use/setup' },
						{ label: 'Migrate a stock stack', slug: 'use/migrate' },
						{ label: 'Pending operations and crashes', slug: 'use/pending' },
						{ label: 'Rename a resource', slug: 'use/rename' },
						{ label: 'Remove a resource', slug: 'use/remove' },
						{ label: 'Recover a stack', slug: 'use/recover' },
						{ label: 'Record store bucket', slug: 'use/bucket' },
						{ label: 'CI/CD', slug: 'use/cicd' },
					],
				},
				{
					label: 'Reference',
					collapsed: true,
					items: [
						{ label: 'Reference', slug: 'reference' },
						{ label: 'The adopt document', slug: 'reference/adopt-json' },
						{ label: 'Claims and runs', slug: 'claims' },
					],
				},
			],
		}),
	],
});
