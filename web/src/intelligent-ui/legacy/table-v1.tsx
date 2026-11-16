"use client";
import {useState} from 'react';
import {t} from '../../i18n';
import type {z} from 'zod';
import {tableSchema} from '../table-schema';
import {AnswerSurface} from '../surface';
import {useUIState} from '../state';
import {useInteractionGroup} from '../group';
import {csv,download,safeName} from '../exports';
import {Icon} from '../icons';
import {Input} from '../../components/ui/answer-controls';
// Retained only for original graphs without the Table service (UI1.13 and
// earlier references, in-flight Runs and previews). Managed failures never use
// this path. Remove when those retained consumers and references have retired.
export function LegacyDataTable({props,reference=''}:{props:z.infer<typeof tableSchema>;reference?:string}){
 const parsed=tableSchema.safeParse(props);const group=useInteractionGroup();const[state,update,saved]=useUIState(reference,{sort:-1,direction:1},s=>({title:props.title,sort:s.sort>=0?props.columns[s.sort]:null,direction:s.direction}));const [filterOpen,setFilterOpen]=useState(false);
 if(!parsed.success||parsed.data.rows.some(row=>row.length!==parsed.data.columns.length))return <p role="alert">{t('This table has invalid rows or columns. Ask the assistant to regenerate it.')}</p>;
 const{title,columns,rows,source}=parsed.data;
 const numeric=columns.map((_,index)=>rows.some(row=>row[index].trim()!=='')&&rows.every(row=>row[index].trim()===''||Number.isFinite(Number(row[index]))));
 const filtered=rows.filter(row=>(!group.query||row.some(cell=>cell.toLocaleLowerCase().includes(group.query.toLocaleLowerCase())))&&(!group.selected||row.includes(group.selected)));
 const visible=state.sort<0?filtered:[...filtered].sort((a,b)=>state.direction*a[state.sort].localeCompare(b[state.sort],undefined,{numeric:true,sensitivity:'base'}));
 return <AnswerSurface title={title} actions={[{label:'Filter',icon:<Icon name="filter"/>,run:()=>setFilterOpen(!filterOpen)},{label:'Reset',icon:<Icon name="reset"/>,run:()=>{group.setQuery('');if(group.selected)group.select(group.selected);update({sort:-1,direction:1})}},{label:'Download CSV',icon:<Icon name="download"/>,run:()=>download(csv([columns,...visible]),'text/csv;charset=utf-8',safeName(title)+'.csv')}]}>
  {(filterOpen||group.query)&&<div className="answer-filter"><Input type="search" aria-label={t('Filter rows')} placeholder={t('Filter')} value={group.query} onChange={event=>group.setQuery(event.target.value)}/></div>}
  <section className="answer-table" aria-label={title}><div className="answer-table-scroll" role="region" aria-label={`${title} ${t('table')}`} tabIndex={0}><table><caption>{title}</caption><thead><tr>{columns.map((column,index)=><th key={index} scope="col" data-numeric={numeric[index]} aria-sort={state.sort===index?(state.direction===1?'ascending':'descending'):'none'}><button type="button" className="answer-column-sort" onClick={()=>update({sort:index,direction:state.sort===index?-state.direction:1})}>{column}{state.sort===index&&<Icon name="sort"/>}</button></th>)}</tr></thead><tbody>{visible.map((row,index)=><tr key={index} aria-selected={group.selected?row.includes(group.selected):undefined}>{row.map((cell,column)=><td key={column} data-numeric={numeric[column]}>{column===0?<button type="button" className="answer-cell-select" aria-pressed={group.selected===cell} onClick={()=>group.select(cell)}>{cell}</button>:cell}</td>)}</tr>)}</tbody></table>{!visible.length&&<p>{t('No rows to display.')}</p>}</div><p className="answer-table-source">{source}</p></section>
  {saved.error&&<p className="field-help" role="alert">{t(saved.error)}</p>}
 </AnswerSurface>
}
