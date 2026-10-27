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

/**
 * Every way a plunge stack behaves differently from a stock Pulumi stack, checked
 * against the code: autoname.go and plunge_autonaming.go (delete first),
 * history.go, backend.go (EnvRefresh), deletevet.go, url.go and inject.go. The
 * picker, the rooms and /use/migrate/ show this list; keep them in step.
 */
export const DIFFERS: string[] = [
	'Replacing a resource whose name plunge chose deletes the old object before it creates the new one, so for a short window neither exists (#12).',
	'plunge keeps no stack history and no checkpoint backups. Bucket versioning keeps earlier versions of each record.',
	'plunge preview and plunge up refresh from the cloud first, by default. PLUNGE_REFRESH=false turns that off.',
	'A new resource the program leaves unnamed gets a fixed name, its name plus seven hex digits, in place of a random suffix.',
	'A delete, plunge destroy included, is refused when a taggable object has lost its plunge-stack and plunge-urn markers.',
	'The backend URL gains a plunge+ prefix, and each taggable resource gets plunge-stack and plunge-urn tags.',
];

const cloudDiffers = 'plunge has no hosted service. Issue #8 lists each Pulumi Cloud feature you lose.';

const migrate = { label: 'Migrate a stock stack', href: '/use/migrate/' };
const quickstart = { label: 'Five-minute quickstart', href: '/use/start/#quickstart' };
const start = { label: 'Start a new stack', href: '/use/start/' };
const tutorial = { label: 'Tutorial', href: '/use/tutorial/' };
const setup = { label: 'Set up by hand', href: '/use/setup/' };
const overview = { label: 'Overview', href: '/how-it-works/' };
const records = { label: 'Records', href: '/how-it-works/records/' };
const concurrency = { label: 'Resource-level concurrency', href: '/how-it-works/concurrency/' };
const glossary = { label: 'Glossary', href: '/glossary/' };
const reference = { label: 'Commands, URLs and variables', href: '/reference/' };
const claims = { label: 'Claims and runs', href: '/claims/' };

/** The tiles under "You run today". Each sends you to a room, with its own first step. */
export const FROM: { id: string; label: string; blurb: string; room: string; first: Link; differs: string[] }[] = [
	{ id: 'stack', label: 'Pulumi, DIY backend', blurb: 'A file, S3, GCS or Azure Blob backend', room: 'pulumi-stack', first: migrate, differs: [] },
	{ id: 'cloud', label: 'Pulumi Cloud', blurb: 'The hosted backend', room: 'pulumi-stack', first: migrate, differs: [cloudDiffers] },
	{ id: 'new', label: 'Nothing yet', blurb: 'A new stack', room: 'new', first: quickstart, differs: [] },
];

/** The tiles under "Targets". A status says how far the platform's proof page goes. */
export const PLATFORMS: { id: Platform; label: string; blurb: string; status: string; hub: string; differs: string[] }[] = [
	{ id: 'aws', label: 'AWS', blurb: 'Tags and Cloud Control', status: 'Emulator runs', hub: '/aws/', differs: [] },
	{
		id: 'kubernetes',
		label: 'Kubernetes',
		blurb: 'Labels and Secrets',
		status: 'Not run yet',
		hub: '/kubernetes/',
		differs: ['No corpus program covers Kubernetes, and nothing has run on it yet.'],
	},
	{
		id: 'gcp',
		label: 'GCP',
		blurb: 'Labels and service lists',
		status: 'Emulator runs',
		hub: '/gcp/',
		differs: ['Only Cloud Storage listings back the collision check. Other services bind, with consistency unmeasured.'],
	},
	{ id: 'azure', label: 'Azure', blurb: 'Tags and ARM', status: 'Emulator runs', hub: '/azure/', differs: ['ARM listings bind, with consistency unmeasured (#15).'] },
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
			'Install v0.1.0 from its release archive',
		],
		differs: DIFFERS,
		first: quickstart,
		howto: [tutorial, start, setup],
		explain: [overview, records, { label: 'Names', href: '/how-it-works/names/' }],
		reference: [glossary, reference],
	},
	{
		id: 'pulumi-stack',
		title: 'Migrate a Pulumi stack',
		lede: 'You run Pulumi today, on a DIY backend (a directory, S3, GCS or Azure Blob) or on Pulumi Cloud.',
		works: [
			'plunge stack adopt --json classifies your checkpoint and writes nothing (recorded on floci)',
			'Adoption keeps every ID and gives each resource its own record',
			'Adopting the same checkpoint again does nothing',
			'A stock pulumi stack export from Pulumi Cloud gives the file plunge stack adopt --file takes',
		],
		differs: [...DIFFERS, cloudDiffers, 'Adopting a corpus stack end to end, and backing out, wait for a run (#7).'],
		first: migrate,
		howto: [migrate, setup, { label: 'Run it from CI', href: '/use/cicd/' }],
		explain: [overview, records],
		reference: [{ label: 'The adopt document', href: '/reference/adopt-json/' }, { label: 'Leaving Pulumi Cloud (#8)', href: issue(8) }, reference],
	},
	{
		id: 'evaluate',
		title: 'Evaluate',
		lede: 'You want to know which parts of plunge have been shown to work.',
		works: [
			'Four runs are recorded, each with its date and its files, all on local emulators',
			'Lock-free concurrent updates are unit-tested and wait for a run on a real store (#6)',
			'scripts/plunge-baseline.sh compares plunge with stock Pulumi on seven corpus programs',
		],
		differs: DIFFERS,
		first: claims,
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
