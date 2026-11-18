import {connectBrowserProgram} from './browser-program';
import type {TracePageInput,TraceNavigationInput} from './trace-page-controls-contract';
import type {TraceControlsInput} from './trace-workbench-controls-contract';
import type {TraceActivityBucket,TraceActivityState,TraceActivityEvent} from './trace-activity-projection';
// Trace-private transport. Only authorized display metadata, local preference
// strings and time coordinates are accepted; no raw model bodies or effects.
type Window = {start:number;end:number};
export type TraceTimelineAction =
 | {kind:'restore'}
 | {kind:'brush';first:number;last:number}
 | {kind:'button';action:'fit'|'in'|'out'|'selected';selected:{start:number;end:number|null}|null}
 | {kind:'key';key:'ArrowLeft'|'ArrowRight'|'Home'|'+'|'-'}
 | {kind:'wheel';pan:boolean;delta_y:number;delta_x:number;anchor:number};
export type TraceBrowserRequest =
 | {type:'page_controls';input:TracePageInput}
 | {type:'navigation';input:TraceNavigationInput}
 | {type:'request_reader';input:{language:'en'|'zh-CN'}}
 | {type:'workbench_controls';input:TraceControlsInput}
 | {type:'activity';input:{buckets:TraceActivityBucket[];state:TraceActivityState|null;event:TraceActivityEvent;language:'en'|'zh-CN'}}
 | {type:'activity_view';input:{buckets:{at:string;until:string;count:number}[];width:number}}
 | {type:'timeline_brush';input:{domain:Window;current:Window;first:number;last:number}}
 | {type:'timeline';input:{domain:Window;current:Window;action:TraceTimelineAction;width:number;has_selected:boolean;language:'en'|'zh-CN';rows:{start:number;end:number|null;running:boolean;first_output:number|null}[]}}
 | {type:'tree';input:{nodes:{id:string;parent?:string;name:string;type:string}[];query:string;closed:string[];language?:'en'|'zh-CN'}}
 | {type:'column_controls';input:{action:'render';visible:boolean[]}|{action:'toggle';visible:boolean[];index:number;checked:boolean}}
 | {type:'columns';input:Partial<Record<'steward.trace.columns.v2'|'steward.trace.columns.v3'|'steward.trace.columns.v4'|'steward.trace.columns.v5',string>>;unavailable?:('steward.trace.columns.v2'|'steward.trace.columns.v3'|'steward.trace.columns.v4'|'steward.trace.columns.v5')[]}
;
export interface TraceBrowser {
 call(request:TraceBrowserRequest):Promise<unknown>;
 close():void;
}
export async function connectTraceBrowser(revision: string): Promise<TraceBrowser> {
  const errorMessage = 'Trace presentation program unavailable';
  if (!/^sha256:[a-f0-9]{64}$/.test(revision)) throw new Error(errorMessage);
  const {call, close} = await connectBrowserProgram<TraceBrowserRequest, void>({
    src: '/api/developer/traces/program?reader_revision=' + encodeURIComponent(revision),
    title: 'Trace isolated presentation',
    channel: 'steward-trace-browser',
    errorMessage,
    initialize: () => undefined,
    acceptsReply: (reply, requestType) => reply.type === requestType && Object.keys(reply).length === 3 && Object.hasOwn(reply, 'result'),
  });
  return {call: async request => (await call(request)).result, close};
}
