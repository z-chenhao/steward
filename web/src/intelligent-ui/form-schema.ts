import {z} from 'zod';
const fieldSchema=z.object({name:z.string().regex(/^[A-Za-z][A-Za-z0-9_]{0,63}$/),label:z.string().min(1).max(256),type:z.enum(['text','textarea','number','date','email','select','multiselect','boolean']),required:z.boolean().optional(),options:z.array(z.object({value:z.string().min(1).max(200),label:z.string().min(1).max(200)}).strict()).max(32).optional(),min:z.number().optional(),max:z.number().optional()}).strict();
export const formSchema=z.object({title:z.string().min(1).max(300),fields:z.array(fieldSchema).min(1).max(16),submitLabel:z.string().max(100).optional()}).strict();
export type FormProps=z.infer<typeof formSchema>;

export const formViewSchema=z.object({props:formSchema,defaults:z.record(z.string(),z.string().refine(value=>[...value].length<=4096))}).strict().superRefine((view,context)=>{
 const names=new Set(view.props.fields.map(field=>field.name));
 if(names.size!==view.props.fields.length||Object.keys(view.defaults).length!==names.size||Object.keys(view.defaults).some(name=>!names.has(name)))context.addIssue({code:'custom',message:'Invalid form display defaults.'});
});
export type FormView=z.infer<typeof formViewSchema>;
