// The home page picker and the room pages at /for/<id>/. Picker.astro and
// Room.astro both read these lists. An href is a site path ('/use/migrate/')
// or a full https:// address; scripts/check-rooms.mjs fails the build on one
// that does not resolve.
//
// A "works" line names a behaviour with a recorded run (/claims/) or says it
// waits for one and which issue holds it (INTENTIUS/plunge#44).

export type Link = { label: string; href: string };
export type Platform = 'aws' | 'kubernetes' | 'gcp' | 'azure';

export type Room = {
	id: string;
	title: string;
	lede: string;
	works: string[];
	differs: string[];
	first: Link;
	howto: Link[];
	explain: Link[];
	reference: Link[];
};

const repo = (path: string) => `https://github.com/INTENTIUS/plunge/blob/main/${path}`;
const issue = (n: number) => `https://github.com/INTENTIUS/plunge/issues/${n}`;

/** The tiles. */
export const FROM: { id: string; label: string; blurb: string; room: string }[] = [
	{ id: 'stack', label: 'Pulumi, DIY backend', blurb: 'A file, S3, GCS or Azure Blob backend', room: 'pulumi-stack' },
	{ id: 'cloud', label: 'Pulumi Cloud', blurb: 'The hosted backend', room: 'pulumi-cloud' },
	{ id: 'chant', label: 'chant', blurb: 'A workspace with a pulumi member', room: 'chant' },
	{ id: 'new', label: 'Nothing yet', blurb: 'A new stack', room: 'new' },
];

export const PLATFORMS: { id: Platform; label: string; blurb: string; room: string; hub: string }[] = [
	{ id: 'aws', label: 'AWS', blurb: 'Tags and Cloud Control', room: 'aws', hub: '/aws/' },
	{ id: 'kubernetes', label: 'Kubernetes', blurb: 'Labels and Secrets', room: 'kubernetes', hub: '/kubernetes/' },
	{ id: 'gcp', label: 'GCP', blurb: 'Labels and service lists', room: 'gcp', hub: '/gcp/' },
	{ id: 'azure', label: 'Azure', blurb: 'Tags and ARM', room: 'azure', hub: '/azure/' },
];

export const ROLES: { label: string; room: string }[] = [
	{ label: 'I review security or own access', room: 'security' },
	{ label: 'I am evaluating', room: 'evaluate' },
	{ label: 'I drive a coding agent', room: 'agents' },
	{ label: 'I want to contribute', room: 'contribute' },
];

const migrate = { label: 'Migrate a stock stack', href: '/use/migrate/' };
const start = { label: 'Start a new stack', href: '/use/start/' };
const setup = { label: 'Set up by hand', href: '/use/setup/' };
const overview = { label: 'Overview', href: '/how-it-works/' };
const records = { label: 'Records', href: '/how-it-works/records/' };
const concurrency = { label: 'Resource-level concurrency', href: '/how-it-works/concurrency/' };
const reference = { label: 'Commands, URLs and variables', href: '/reference/' };
const claims = { label: 'Claims and runs', href: '/claims/' };

export const ROOMS: Room[] = [
	{
		id: 'pulumi-stack',
		title: 'From a Pulumi stack',
		lede: 'You run Pulumi on a DIY backend: a local directory, S3, GCS or Azure Blob.',
		works: [
			'plunge stack adopt --json classifies your checkpoint and writes nothing (recorded on AWS)',
			'Adoption keeps every ID and gives each resource its own record',
			'Adopting the same checkpoint again does nothing',
		],
		differs: [
			'The backend URL gains a plunge+ prefix, and the records sit beside the DIY metadata.',
			'The first plunge up after adoption adds the plunge-stack and plunge-urn tags in place.',
			'Adopting a corpus stack end to end, and backing out, wait for a run (#7).',
		],
		first: migrate,
		howto: [migrate, setup, { label: 'Run it from CI', href: '/use/cicd/' }],
		explain: [overview, records],
		reference: [{ label: 'The adopt document', href: '/reference/adopt-json/' }, reference],
	},
	{
		id: 'pulumi-cloud',
		title: 'From Pulumi Cloud',
		lede: 'You keep your stacks in Pulumi Cloud.',
		works: [
			'plunge runs the same programs, SDKs, language hosts and providers as stock Pulumi',
			'A stock pulumi stack export gives the deployment plunge stack adopt --file takes',
		],
		differs: [
			'plunge has no hosted service. #8 lists each Pulumi Cloud feature you lose and whether chant covers it.',
		],
		first: { label: 'Leaving Pulumi Cloud (#8)', href: issue(8) },
		howto: [migrate, start],
		explain: [overview],
		reference: [{ label: 'The adopt document', href: '/reference/adopt-json/' }, reference],
	},
	{
		id: 'chant',
		title: 'From chant',
		lede: 'You run a chant workspace with a Pulumi member.',
		works: [
			'chant picks plunge when Pulumi.yaml names a plunge+ backend URL',
			'chant reads plunge drift --stream as drift record v1 (recorded)',
			'chant reads plunge stack export and stack output (recorded)',
		],
		differs: [],
		first: { label: 'Drift of one resource', href: '/how-it-works/drift/' },
		howto: [start, { label: 'Tutorial', href: '/use/tutorial/' }],
		explain: [overview, records],
		reference: [
			{ label: 'chant drift record v1', href: 'https://github.com/INTENTIUS/chant/blob/main/docs/src/content/docs/reference/drift-record.mdx' },
			reference,
		],
	},
	{
		id: 'new',
		title: 'Starting new',
		lede: 'You are starting a new Pulumi stack.',
		works: [
			'Install v0.1.0 from its release archive',
			'Log in to a plunge+ URL and run plunge up',
			'The tutorial needs Docker, Node.js and a clone of the repository',
		],
		differs: [],
		first: start,
		howto: [{ label: 'Tutorial', href: '/use/tutorial/' }, setup],
		explain: [overview, records, { label: 'Names', href: '/how-it-works/names/' }],
		reference: [reference],
	},
	{
		id: 'aws',
		title: 'On AWS',
		lede: 'You run on AWS, with pulumi-aws or aws-native.',
		works: [
			'Taggable resources carry plunge-stack and plunge-urn tags (recorded on floci)',
			'An unnamed resource gets the same name on every run, from its stack and URN',
			'Cloud Control lists the marked objects before each create',
		],
		differs: [],
		first: { label: 'The AWS hub', href: '/aws/' },
		howto: [{ label: 'Adopt', href: '/aws/adopt/' }, { label: 'Operate', href: '/aws/operate/' }, { label: 'Create the bucket', href: '/use/bucket/' }],
		explain: [{ label: 'Gate', href: '/aws/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/aws/compatibility/' }, { label: 'Proof', href: '/aws/proof/' }],
	},
	{
		id: 'kubernetes',
		title: 'On Kubernetes',
		lede: 'You run Kubernetes with pulumi-kubernetes.',
		works: [
			'Objects carry the markers in metadata.labels, as digests',
			'plunge+k8s:// keeps each record in a Secret',
			'A LIST by label selector finds the marked objects',
		],
		differs: ['No corpus program covers Kubernetes, and nothing has run on it yet.'],
		first: { label: 'The Kubernetes hub', href: '/kubernetes/' },
		howto: [{ label: 'Adopt', href: '/kubernetes/adopt/' }, { label: 'Operate', href: '/kubernetes/operate/' }],
		explain: [{ label: 'Gate', href: '/kubernetes/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/kubernetes/compatibility/' }, { label: 'Proof', href: '/kubernetes/proof/' }],
	},
	{
		id: 'gcp',
		title: 'On GCP',
		lede: 'You run on Google Cloud with pulumi-gcp.',
		works: [
			'Labels carry the markers as hex digests',
			'gcp-yaml previews the same under plunge and stock Pulumi (recorded)',
			'156 types list through their own service',
		],
		differs: ['Only Cloud Storage listings back the collision check. Other services bind, with consistency unmeasured.'],
		first: { label: 'The GCP hub', href: '/gcp/' },
		howto: [{ label: 'Adopt', href: '/gcp/adopt/' }, { label: 'Operate', href: '/gcp/operate/' }],
		explain: [{ label: 'Gate', href: '/gcp/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/gcp/compatibility/' }, { label: 'Proof', href: '/gcp/proof/' }],
	},
	{
		id: 'azure',
		title: 'On Azure',
		lede: 'You run on Azure with pulumi-azure or azure-native.',
		works: [
			'Tags carry the markers as readable values',
			'azure-yaml previews the same as stock Pulumi apart from autonamed names (recorded)',
			'ARM lists the marked objects by their plunge-urn tag',
		],
		differs: ['ARM listings bind, with consistency unmeasured (#15).'],
		first: { label: 'The Azure hub', href: '/azure/' },
		howto: [{ label: 'Adopt', href: '/azure/adopt/' }, { label: 'Operate', href: '/azure/operate/' }],
		explain: [{ label: 'Gate', href: '/azure/gate/' }, { label: 'Identity', href: '/how-it-works/identity/' }],
		reference: [{ label: 'Compatibility', href: '/azure/compatibility/' }, { label: 'Proof', href: '/azure/proof/' }],
	},
	{
		id: 'security',
		title: 'Security and access',
		lede: 'You decide who may change which resources.',
		works: [
			'The plunge-stack marker is a tag or label your cloud IAM can condition on',
			'An S3, GCS or Azure Blob store refuses to open without versioning and a public access block, unless you waive the setting by name',
			'Store credentials come from the environment, never from the backend URL',
		],
		differs: [
			'No gate has been run against plunge markers yet (#45).',
			'Records hold the full state, outputs included, so guard the store as you guard a state file.',
		],
		first: { label: 'Identity', href: '/how-it-works/identity/' },
		howto: [
			{ label: 'Gate on AWS', href: '/aws/gate/' },
			{ label: 'Gate on Kubernetes', href: '/kubernetes/gate/' },
			{ label: 'Gate on GCP', href: '/gcp/gate/' },
			{ label: 'Gate on Azure', href: '/azure/gate/' },
			{ label: 'Create the bucket', href: '/use/bucket/' },
		],
		explain: [records, { label: 'Deletes', href: '/how-it-works/deletes/' }],
		reference: [{ label: 'CONCURRENCY.md', href: repo('docs/plunge/CONCURRENCY.md') }, reference],
	},
	{
		id: 'evaluate',
		title: 'Evaluating',
		lede: 'You want to see it work first.',
		works: [
			'Four runs are recorded, each with its date and its files',
			'Seven corpus programs run against local emulators',
			'scripts/plunge-baseline.sh compares plunge with stock Pulumi',
		],
		differs: [],
		first: claims,
		howto: [{ label: 'Tutorial', href: '/use/tutorial/' }, start],
		explain: [overview, concurrency],
		reference: [{ label: 'The corpus', href: repo('tests/plunge-corpus/README.md') }, reference],
	},
	{
		id: 'agents',
		title: 'With a coding agent',
		lede: 'An agent types. You run up, destroy and adoption.',
		works: [
			'llms.txt lists every page',
			'Page prompts leave plunge up, plunge destroy and adoption to you',
			'stack adopt --json and drift --stream print documents a program can read',
		],
		differs: [],
		first: { label: 'llms.txt', href: '/llms.txt' },
		howto: [{ label: 'Tutorial', href: '/use/tutorial/' }, { label: 'Run it from CI', href: '/use/cicd/' }],
		explain: [overview],
		reference: [{ label: 'The adopt document', href: '/reference/adopt-json/' }, reference],
	},
	{
		id: 'contribute',
		title: 'Contributing',
		lede: 'You want to change plunge.',
		works: [
			"plunge-only code lives in new files beside upstream's",
			'Upstream pulumi is merged in, never rebased',
			'plunge-sdk-check asks the engine which features each SDK release needs',
		],
		differs: [],
		first: { label: 'The README', href: repo('README.md') },
		howto: [{ label: 'The corpus', href: repo('tests/plunge-corpus/README.md') }],
		explain: [{ label: 'CONCURRENCY.md', href: repo('docs/plunge/CONCURRENCY.md') }],
		reference: [{ label: 'Design record (#9)', href: issue(9) }, claims],
	},
];

export function room(id: string): Room {
	const r = ROOMS.find((x) => x.id === id);
	if (!r) throw new Error(`src/data/rooms.ts: no room ${id}`);
	return r;
}

/** Room order in the "Start here" sidebar. */
export const ROOM_ORDER: string[] = [...FROM.map((f) => f.room), ...PLATFORMS.map((p) => p.room), ...ROLES.map((r) => r.room)];

/** Every link here, for scripts/check-rooms.mjs. */
export function roomLinks(): (Link & { where: string })[] {
	const out: (Link & { where: string })[] = [];
	for (const r of ROOMS) {
		for (const l of [r.first, ...r.howto, ...r.explain, ...r.reference]) out.push({ ...l, where: `room ${r.id}` });
	}
	for (const p of PLATFORMS) out.push({ label: p.label, href: p.hub, where: 'PLATFORMS' });
	return out;
}
