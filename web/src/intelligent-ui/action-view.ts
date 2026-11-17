import {z} from 'zod';
const text=z.string().min(1).max(300);
export const actionViewSchema=z.object({component:z.number().int().min(0).max(15),working_label:text,unconfirmed_error:text,unknown_error:text,cancelled_error:text,not_executed_error:text,failed_error:text,success_indicator:z.boolean()}).strict();
export type ActionView=z.infer<typeof actionViewSchema>;
