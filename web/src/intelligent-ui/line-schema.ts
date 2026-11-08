import {z} from 'zod';

const number=z.number().finite().min(-1e15).max(1e15);
const base={title:z.string().max(300),description:z.string().max(2000)};
export const lineChartSchema=z.union([
  z.object({...base,data:z.array(z.object({label:z.string().max(200),value:number}).strict()).max(200)}).strict(),
  z.object({...base,unit:z.string().max(64).optional(),labels:z.array(z.string().max(200)).max(200),series:z.array(z.object({name:z.string().min(1).max(200).refine(s=>s.trim().length>0),values:z.array(number).max(200)}).strict()).min(1).max(6)}).strict().refine(p=>new Set(p.series.map(s=>s.name)).size===p.series.length&&p.series.every(s=>s.values.length===p.labels.length)),
]);
