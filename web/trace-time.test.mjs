import assert from 'node:assert/strict';
import test from 'node:test';
import {buildSync} from 'esbuild';
import {runInNewContext} from 'node:vm';
const source=buildSync({entryPoints:[new URL('src/trace-time.ts',import.meta.url).pathname],bundle:true,format:'cjs',write:false}).outputFiles[0].text;
const module={exports:{}};runInNewContext(source,{module,exports:module.exports});
const {boundedWindow,zoomWindow,bucketSelection}=module.exports;
test('timeline zoom keeps the pointer anchor fixed and stays inside the whole trace',()=>{
 const domain={start:100,end:1100};const zoom=zoomWindow(domain,domain,.5,.25);
 assert.equal(zoom.start,225);assert.equal(zoom.end,725);
 const whole=zoomWindow(domain,zoom,10);assert.equal(whole.start,100);assert.equal(whole.end,1100);
 const pan=boundedWindow(domain,1000,1200);assert.equal(pan.start,900);assert.equal(pan.end,1100);
});
test('reverse histogram brushing includes both endpoint buckets without escaping data',()=>{
 const range=bucketSelection(48,30,4);assert.equal(range.first,4);assert.equal(range.last,30);
 const clipped=bucketSelection(48,-8,100);assert.equal(clipped.first,0);assert.equal(clipped.last,47);
});

const {activityGeometry,activityIndex,timeMicros}=module.exports;
test('clipped time buckets keep proportional geometry and exact microsecond boundaries',()=>{
 const buckets=[{at:'2026-11-08T00:00:00.000500Z',until:'2026-11-08T00:00:00.001000Z'},{at:'2026-11-08T00:00:00.001000Z',until:'2026-11-08T00:00:00.002000Z'},{at:'2026-11-08T00:00:00.002000Z',until:'2026-11-08T00:00:00.002500Z'}];
 const geometry=activityGeometry(buckets);
 assert.equal(timeMicros(buckets[0].until)-timeMicros(buckets[0].at),500);
 assert.equal(geometry[0].left,0);assert.equal(geometry[0].right,.25);assert.equal(geometry[1].left,.25);assert.equal(geometry[1].right,.75);assert.equal(geometry[2].right,1);
 assert.equal(activityIndex(geometry,.249),0);assert.equal(activityIndex(geometry,.25),1);assert.equal(activityIndex(geometry,.749),1);assert.equal(activityIndex(geometry,.75),2);assert.equal(activityIndex(geometry,1),2);
});
