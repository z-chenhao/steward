// Time-domain arithmetic shared by the histogram and execution timeline.
export type TimeWindow={start:number;end:number};
export function boundedWindow(domain:TimeWindow,start:number,end:number):TimeWindow {
 const span=Math.min(domain.end-domain.start,Math.max(1,end-start));
 const left=Math.max(domain.start,Math.min(start,domain.end-span));
 return {start:left,end:left+span};
}
export function zoomWindow(domain:TimeWindow,current:TimeWindow,factor:number,anchor=.5):TimeWindow {
 const span=(current.end-current.start)*factor,pivot=current.start+(current.end-current.start)*anchor;
 return boundedWindow(domain,pivot-span*anchor,pivot+span*(1-anchor));
}
export function bucketSelection(count:number,first:number,last:number){
 return {first:Math.max(0,Math.min(count-1,Math.min(first,last))),last:Math.max(0,Math.min(count-1,Math.max(first,last)))};
}
export function elapsedLabel(microseconds:number){
 const ms=Math.max(0,microseconds/1000);
 return ms<1000?`${ms.toFixed(2)}ms`:`${(ms/1000).toFixed(2)}s`;
}

// Date.parse truncates sub-millisecond timestamps. Keep the microsecond part
// used by the durable ledger so cropped histogram boundaries stay exact.
export function timeMicros(raw:string){
 const fraction=raw.match(/\.(\d+)(?:Z|[+-]\d{2}:\d{2})$/)?.[1]??'';
 return Date.parse(raw)*1000+Number(fraction.padEnd(6,'0').slice(3,6));
}
export function activityGeometry(buckets:{at:string;until:string}[]){
 const start=timeMicros(buckets[0].at),end=timeMicros(buckets.at(-1)!.until),span=Math.max(1,end-start);
 return buckets.map(bucket=>({left:(timeMicros(bucket.at)-start)/span,right:(timeMicros(bucket.until)-start)/span}));
}
export function activityIndex(geometry:{left:number;right:number}[],fraction:number){
 const index=geometry.findIndex(bucket=>fraction<bucket.right);
 return index<0?geometry.length-1:index;
}
