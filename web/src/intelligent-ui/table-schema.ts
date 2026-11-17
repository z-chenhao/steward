import {z} from 'zod';
export const tableSchema=z.object({title:z.string().max(300),columns:z.array(z.string().max(200)).min(1).max(12),rows:z.array(z.array(z.string().max(2000)).max(12)).max(200),source:z.string().max(1000).describe('Data provenance: user-provided, calculated, sourced, or explicitly illustrative.')});
