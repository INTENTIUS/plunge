// The home page picker and the room pages at /for/<id>/. Picker.astro and
// Room.astro both read these lists. An href is a site path ('/use/migrate/')
// or a full https:// address; scripts/check-rooms.mjs fails the build on one
// that does not resolve.
//
// A "works" line names a behaviour plunge has.

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

/**
 * Every way a plunge stack behaves differently from a stock Pulumi stack, checked
 * against the code: autoname.go and plunge_autonaming.go (delete first),
 * history.go, backend.go (EnvRefresh), deletevet.go, url.go and inject.go. The
 * picker, the rooms and /use/migrate/ show this list; keep them in step.
 */
export const DIFFERS: string[] = [
	'A plunge+ backend URL, and two marker tags on each resource.',
	'Unnamed resources get a fixed name in place of a random suffix.',
	'Deletes check the markers first.',
	'No stack history; bucket versioning keeps each record\'s versions.',
];

const cloudDiffers = 'Your hosted stacks stay where they are until you adopt one. Issue #8 lists each Pulumi Cloud feature and how chant works next to it.';

const migrate = { label: 'Adopt an existing stack', href: '/use/migrate/' };
const quickstart = { label: 'Five-minute quickstart', href: '/use/start/#quickstart' };
const start = { label: 'Start a new stack', href: '/use/start/' };
const tutorial = { label: 'Tutorial', href: '/use/tutorial/' };
const setup = { label: 'Set up by hand', href: '/use/setup/' };
const overview = { label: 'Overview', href: '/how-it-works/' };
const records = { label: 'Records', href: '/how-it-works/records/' };
const concurrency = { label: 'Resource-level concurrency', href: '/how-it-works/concurrency/' };
const glossary = { label: 'Glossary', href: '/glossary/' };
const reference = { label: 'Commands, URLs and variables', href: '/reference/' };

/** The tiles under "You run today". Each sends you to a room, with its own first step. */
export const FROM: { id: string; label: string; blurb: string; room: string; first: Link; differs: string[] }[] = [
	{ id: 'stack', label: 'Pulumi, DIY backend', blurb: 'A file, S3, GCS or Azure Blob backend', room: 'pulumi-stack', first: migrate, differs: [] },
	{ id: 'cloud', label: 'Pulumi Cloud', blurb: 'The hosted backend', room: 'pulumi-stack', first: migrate, differs: [cloudDiffers] },
	{ id: 'new', label: 'Nothing yet', blurb: 'A new stack', room: 'new', first: quickstart, differs: [] },
];

/** The tiles under "Targets". */
export const PLATFORMS: { id: Platform; label: string; blurb: string; hub: string; differs: string[] }[] = [
	{ id: 'aws', label: 'AWS', blurb: 'Tags and Cloud Control', hub: '/aws/', differs: [] },
	{
		id: 'kubernetes',
		label: 'Kubernetes',
		blurb: 'Labels and Secrets',
		hub: '/kubernetes/',
		differs: [],
	},
	{
		id: 'gcp',
		label: 'GCP',
		blurb: 'Labels and service lists',
		hub: '/gcp/',
		differs: ['Only Cloud Storage listings back the collision check. Other services bind, with consistency unmeasured.'],
	},
	{ id: 'azure', label: 'Azure', blurb: 'Tags and ARM', hub: '/azure/', differs: ['ARM listings bind, with consistency unmeasured (#15).'] },
];

export const ROLES: Link[] = [
	{ label: 'I review security or own access', href: '/how-it-works/identity/' },
	{ label: 'I am evaluating', href: '/for/evaluate/' },
	{ label: 'I drive a coding agent', href: '/llms.txt' },
	{ label: 'I want to contribute', href: repo('README.md') },
];

export const ROOMS: Room[] = [
	{
		id: 'new',
		title: 'Try it',
		lede: 'You want to see plunge run, or you are starting a new stack.',
		works: [
			'The quickstart runs offline in five minutes, on a local backend with no cloud account',
			'The tutorial runs a corpus program on floci, a local AWS emulator, and needs Docker, Node.js, git and the AWS CLI',
			'Install v0.2.0 from its release archive',
		],
		differs: DIFFERS,
		first: quickstart,
		howto: [tutorial, start, setup],
		explain: [overview, records, { label: 'Names', href: '/how-it-works/names/' }],
		reference: [glossary, reference],
	},
	{
		id: 'pulumi-stack',
		title: 'Adopt a Pulumi stack',
		lede: 'You run Pulumi today, on a DIY backend (a directory, S3, GCS or Azure Blob) or on Pulumi Cloud.',
		works: [
			'plunge stack adopt --json classifies your checkpoint and writes nothing',
			'Adoption keeps every ID and gives each resource its own record',
			'Adopting the same checkpoint again does nothing',
			'A stock pulumi stack export from Pulumi Cloud gives the file plunge stack adopt --file takes',
			'Keep the hosted backend for the stacks it runs today, and try one exported stack first',
		],
		differs: [...DIFFERS, cloudDiffers],
		first: migrate,
		howto: [migrate, setup, { label: 'Run it from CI', href: '/use/cicd/' }],
		explain: [overview, records],
		reference: [{ label: 'The adopt document', href: '/reference/adopt-json/' }, { label: 'Pulumi Cloud features and chant (#8)', href: issue(8) }, reference],
	},
	{
		id: 'evaluate',
		title: 'Evaluate',
		lede: 'You are evaluating plunge and want to see what it does.',
		works: [
			'Runs on different resources of one stack proceed together, with no stack lock',
			'scripts/plunge-baseline.sh compares plunge with stock Pulumi on seven corpus programs',
		],
		differs: DIFFERS,
		first: quickstart,
		howto: [quickstart, tutorial],
		explain: [overview, concurrency],
		reference: [{ label: 'The corpus', href: repo('tests/plunge-corpus/README.md') }, glossary, reference],
	},
];

export function room(id: string): Room {
	const r = ROOMS.find((x) => x.id === id);
	if (!r) throw new Error(`src/data/rooms.ts: no room ${id}`);
	return r;
}

/** Room order in the "Start here" sidebar. */
export const ROOM_ORDER: string[] = ROOMS.map((r) => r.id);

/** Every link here, for scripts/check-rooms.mjs. */
export function roomLinks(): (Link & { where: string })[] {
	const out: (Link & { where: string })[] = [];
	for (const r of ROOMS) {
		for (const l of [r.first, ...r.howto, ...r.explain, ...r.reference]) out.push({ ...l, where: `room ${r.id}` });
	}
	for (const f of FROM) out.push({ ...f.first, where: `FROM ${f.id}` });
	for (const p of PLATFORMS) out.push({ label: p.label, href: p.hub, where: 'PLATFORMS' });
	for (const r of ROLES) out.push({ ...r, where: 'ROLES' });
	return out;
}
