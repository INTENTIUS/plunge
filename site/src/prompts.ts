// The prompts handed to a coding agent. Every prompt carries neverLine word for
// word, so none can drop the part that keeps up, destroy, adoption and merge with
// the person. A page's prompt names its own page's URL.

export const SITE = 'https://intentius.io/plunge';

export const neverLine =
	'Never run `plunge up`, `plunge destroy` or `plunge stack adopt --yes`, approve or apply a saved plan, or merge; never edit or remove a plunge-stack or plunge-urn tag or label by hand.';

// A page's `prompt:` front matter, as its text.
export function promptText(prompt: string | undefined): string | undefined {
	const text = prompt?.trim();
	return text ? text : undefined;
}
