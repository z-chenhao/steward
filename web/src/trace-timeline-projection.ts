export interface TraceTimelineProjection {
 ticks:string[];
 drag_threshold:number;
 range_label:string;
 grid:number[];
 full:boolean;
 buttons:{in:boolean;out:boolean;fit:boolean;selected:boolean};
 rows:{bar:{x:number;width:number}|null;point:number|null;first_output:number|null;tooltip:string[]}[];
}
const record=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
const coordinate=(value:unknown)=>typeof value==='number'&&Number.isFinite(value)&&value>=0&&value<=1000;
export function acceptTraceTimelineProjection(value:unknown,count:number,full:boolean):TraceTimelineProjection {
 if(!record(value)||typeof value.drag_threshold!=='number'||!Number.isFinite(value.drag_threshold)||value.drag_threshold<0||value.drag_threshold>64||!Array.isArray(value.ticks)||value.ticks.length<2||value.ticks.length>16||!value.ticks.every(tick=>typeof tick==='string'&&tick.length<=128)||typeof value.range_label!=='string'||value.range_label.length>512||!Array.isArray(value.grid)||value.grid.length>16||!value.grid.every(coordinate)||value.full!==full||!record(value.buttons)||!['in','out','fit','selected'].every(key=>typeof value.buttons==='object'&&value.buttons!==null&&typeof (value.buttons as Record<string,unknown>)[key]==='boolean')||!Array.isArray(value.rows)||value.rows.length!==count)throw Error('invalid timeline projection');
 for(const row of value.rows){
  if(!record(row)||!Array.isArray(row.tooltip)||row.tooltip.length>8||!row.tooltip.every(line=>typeof line==='string'&&line.length<=512)||row.point!==null&&!coordinate(row.point)||row.first_output!==null&&!coordinate(row.first_output)||row.bar!==null&&(!record(row.bar)||!coordinate(row.bar.x)||!coordinate(row.bar.width)||Number(row.bar.width)<1))throw Error('invalid timeline lane');
 }
 return value as unknown as TraceTimelineProjection;
}

export interface TraceTimelineBrush {x:number;width:number;label:string}
export function acceptTraceTimelineBrush(value:unknown):TraceTimelineBrush {
 if(!record(value)||!coordinate(value.x)||!coordinate(value.width)||typeof value.label!=='string'||value.label.length>512)throw Error('invalid timeline brush');
 return value as unknown as TraceTimelineBrush;
}
