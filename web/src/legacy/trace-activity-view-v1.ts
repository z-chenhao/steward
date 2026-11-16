// Only original 1.0.1 graphs and never-managed/direct readers use this policy.
// Remove with those consumers; managed failures never enter this path.
export function legacyTraceActivityView(buckets:{at:string;until:string;count:number}[],width:number){
 const first=Date.parse(buckets[0].at),end=Date.parse(buckets.at(-1)!.until);
 return {maximum:Math.max(...buckets.map(bucket=>bucket.count)),axis:{mode:end-first<86400000?'clock' as const:'date' as const,times:(width<500?[0,1]:[0,.25,.5,.75,1]).map(position=>new Date(first+(end-first)*position).toISOString())}};
}
