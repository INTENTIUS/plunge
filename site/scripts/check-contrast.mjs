// Runs after `astro build` (npm run build) and fails it when a colour pair in
// src/styles/plunge.css drops under WCAG AA: 4.5:1 for text, 3:1 for hairlines
// and other UI. It reads the dark tokens (the first :root block that sets
// --sl-color-white) and the light ones (:root[data-theme='light']), so a token
// changed without a check here fails the build. Ratios are WCAG 2 relative
// luminance contrast.
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('../src/styles/plunge.css', import.meta.url), 'utf8');

const block = (selector) => {
	const re = new RegExp(`${selector.replace(/[[\]()'.]/g, '\\$&')}\\s*\\{([^}]*)\\}`, 'g');
	for (const m of css.matchAll(re)) if (m[1].includes('--sl-color-white')) return m[1];
	throw new Error(`plunge.css: no ${selector} block sets --sl-color-white`);
};
const tokens = (body) => Object.fromEntries([...body.matchAll(/(--[\w-]+):\s*(#[0-9a-fA-F]{6})/g)].map((m) => [m[1], m[2]]));

const lum = (hex) => {
	const [r, g, b] = [1, 3, 5].map((i) => {
		const c = parseInt(hex.slice(i, i + 2), 16) / 255;
		return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
	});
	return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const ratio = (a, b) => {
	const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
	return (hi + 0.05) / (lo + 0.05);
};

// [foreground, background, floor, what]
const PAIRS = [
	['--sl-color-white', '--sl-color-black', 4.5, 'headings on the page'],
	['--sl-color-gray-1', '--sl-color-gray-6', 4.5, 'inline code and aside text on gray-6'],
	['--sl-color-gray-2', '--sl-color-black', 4.5, 'body text on the page'],
	['--sl-color-gray-2', '--pl-panel', 4.5, 'body text on a panel'],
	['--sl-color-gray-3', '--sl-color-black', 4.5, 'secondary text on the page'],
	['--sl-color-gray-3', '--pl-panel', 4.5, 'secondary text on a panel'],
	['--pl-accent', '--sl-color-black', 4.5, 'links on the page'],
	['--pl-accent', '--pl-panel', 4.5, 'links on a panel'],
	['--pl-accent', '--sl-color-gray-6', 4.5, 'links on gray-6'],
	['--sl-color-black', '--pl-accent', 4.5, 'button text on the accent'],
	['--sl-color-accent-high', '--sl-color-black', 4.5, 'accent-high text on the page'],
	['--sl-color-accent-high', '--sl-color-gray-6', 4.5, 'aside links on gray-6'],
	['--pl-sand', '--sl-color-black', 3, 'sand hairlines on the page'],
	['--pl-sand', '--pl-panel', 3, 'sand hairlines on a panel'],
	['--pl-warn', '--sl-color-black', 4.5, 'caution text on the page'],
	['--pl-warn', '--sl-color-gray-6', 3, 'caution edge on an aside'],
];

let failed = 0;
for (const [theme, selector] of [['dark', ':root'], ['light', ":root[data-theme='light']"]]) {
	const t = tokens(block(selector));
	for (const [fg, bg, floor, what] of PAIRS) {
		if (!t[fg] || !t[bg]) {
			console.error(`contrast: ${theme} sets no ${t[fg] ? bg : fg}`);
			failed++;
			continue;
		}
		const r = ratio(t[fg], t[bg]);
		const ok = r >= floor;
		if (!ok) failed++;
		const line = `contrast: ${theme} ${ok ? 'ok  ' : 'FAIL'} ${r.toFixed(2)}:1 (floor ${floor}) ${fg} ${t[fg]} on ${bg} ${t[bg]}: ${what}`;
		(ok ? console.log : console.error)(line);
	}
}
if (failed) process.exit(1);
