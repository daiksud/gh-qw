import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';
import { docsSchema } from '@astrojs/starlight/schema';
import { z } from 'astro/zod';

const docs = defineCollection({
  loader: glob({
    base: '../docs',
    pattern: '**/README.md',
    generateId: ({ entry }) =>
      entry === 'README.md' ? 'index' : entry.replace(/\/README\.md$/i, '/index'),
  }),
  schema: docsSchema({
    extend: z.object({
      type: z.enum(['index', 'concept', 'reference', 'adr', 'template']),
      resource: z.literal('gh-qw'),
      tags: z.array(z.string()),
      timestamp: z.coerce.date(),
    }),
  }),
});

export const collections = { docs };
