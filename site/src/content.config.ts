import { defineCollection } from 'astro:content';
import { z } from 'astro/zod';
import { docsLoader, i18nLoader } from '@astrojs/starlight/loaders';
import { docsSchema, i18nSchema } from '@astrojs/starlight/schema';
import { promptText } from './prompts';

export const collections = {
	// Starlight reads UI strings from this collection. src/content/i18n/en.json
	// keeps one override; without the collection every build warns that it is missing.
	i18n: defineCollection({ loader: i18nLoader(), schema: i18nSchema() }),
	docs: defineCollection({
		loader: docsLoader(),
		schema: docsSchema({
			extend: z.object({
				// "Optional: hand this page to your coding agent": the prompt the page
				// header shows under the title, and llms.txt and llms-full.txt carry.
				// It names the page's own URL and ends with neverLine from src/prompts.ts.
				prompt: z.string().optional().transform(promptText),
			}),
		}),
	}),
};
