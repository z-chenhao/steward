// Only refs/preview graphs without ui-chart-view/1 use this implementation.
// Remove with those historical consumers; managed failures never enter it.
import {t} from '../../i18n';
import {useState} from 'react';
import {ChartDrawing,type ChartData} from '../charts';
import {AnswerSurface} from '../surface';
import {useInteractionGroup} from '../group';
import {useUIState} from '../state';
import {csv,download,safeName} from '../exports';
import {Icon} from '../icons';
export function LegacyChartExplorer({kind,title,description,data:inputData,labels,xValues,series=[],unit='',reference=''}:{kind:'bar'|'pie'|'line'|'scatter';title:string;description:string;data?:ChartData[];labels?:string[];xValues?:number[];series?:{name:string;values:number[]}[];unit?:string;reference?:string}){
 const numericLine=kind==='line'&&xValues!==undefined;
 const data:ChartData[]=inputData??(labels??[]).map((label,i)=>Object.fromEntries([['label',label],['axisX',xValues?.[i]],...series.map((s,j)=>['series'+j,s.values[i]])]) as ChartData);
 const group=useInteractionGroup();const[zoom,setZoom]=useState(false);const[state,update,saved]=useUIState(reference,{start:0,end:Math.max(0,data.length-1)},s=>({title,range:[s.start,s.end]}));
 const filtered=data.filter(d=>!group.query||d.label.toLocaleLowerCase().includes(group.query.toLocaleLowerCase()));const shown=zoom?filtered.slice(state.start,state.end+1):filtered;
 const horizontal=kind==='bar'&&(shown.length>6||shown.some(d=>d.label.length>16));
 const exportRows=kind==='scatter'?[['Label','X','Y'],...shown.map(d=>[d.label,d.x,d.y])]:series.length?[['Label',...(numericLine?['X']:[]),...series.map(s=>s.name+(unit?' ('+unit+')':''))],...shown.map(d=>[d.label,...(numericLine?[d.axisX]:[]),...series.map((_,i)=>d['series'+i])])]:[['Label','Value'],...shown.map(d=>[d.label,d.value])];
 return <AnswerSurface title={title} graphic actions={[{label:'Zoom',icon:<Icon name="zoom"/>,disabled:kind==='pie',run:()=>setZoom(!zoom)},{label:'Reset',icon:<Icon name="reset"/>,run:()=>{setZoom(false);update({start:0,end:Math.max(0,data.length-1)});group.setQuery('');if(group.selected)group.select(group.selected)}},{label:'Download CSV',run:()=>download(csv(exportRows),'text/csv;charset=utf-8',safeName(title)+'.csv')}] }>
  <ChartDrawing kind={kind} title={title} description={description} shown={shown} series={series} unit={unit} horizontal={horizontal} numericLine={numericLine} height={horizontal?Math.max(260,Math.min(640,shown.length*34+40)):260} selection={shown.map(point=>group.selected===point.label)} opacity={shown.map(point=>!group.selected||group.selected===point.label?1:.3)} onSelect={group.select}>
  {zoom&&filtered.length>1&&<div className="answer-chart-range"><label>{t('From')}<input type="range" min="0" max={Math.max(0,state.end)} value={state.start} onChange={event=>update({start:Number(event.target.value)})}/></label><label>{t('To')}<input type="range" min={state.start} max={filtered.length-1} value={state.end} onChange={event=>update({end:Number(event.target.value)})}/></label></div>}
  </ChartDrawing>{saved.error&&<p className="field-help" role="alert">{t(saved.error)}</p>}
 </AnswerSurface>
}
