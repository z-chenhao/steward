export type TracePageState={compact:boolean|null;wide_open:boolean};
export type TracePageInput={list:boolean;width:number;open:boolean;state:TracePageState};
export type TracePagePlan={open:boolean;state:TracePageState;kinds:('select'|'trace'|'identifier'|'context')[];row:boolean;focus_row:boolean};
export function acceptTracePagePlan(value:unknown):TracePagePlan {
 if(!value||typeof value!=='object'||Array.isArray(value))throw Error('invalid page controls');
 const p=value as Partial<TracePagePlan>,s=p.state;
 if(typeof p.open!=='boolean'||!s||typeof s!=='object'||s.compact!==null&&typeof s.compact!=='boolean'||typeof s.wide_open!=='boolean'||!Array.isArray(p.kinds)||p.kinds.length>4||new Set(p.kinds).size!==p.kinds.length||!p.kinds.every(kind=>['select','trace','identifier','context'].includes(kind))||typeof p.row!=='boolean'||typeof p.focus_row!=='boolean')throw Error('invalid page controls');
 return p as TracePagePlan;
}

// Private transition facts only; URLs, IDs, filters and evidence stay local.
export type TraceNavigationInput={list:boolean;current:boolean;same_trace:boolean;same_selection:boolean};
export type TraceNavigationPlan={presentation:'sheet'|'replace'|'page';keep_filter:boolean;keep_tree_scroll:boolean;keep_detail_scroll:boolean;restore_focus:boolean;reveal_tabs:boolean};
export function acceptTraceNavigationPlan(value:unknown,input:TraceNavigationInput):TraceNavigationPlan {
 if(!value||typeof value!=='object'||Array.isArray(value))throw Error('invalid trace navigation');
 const p=value as Record<string,unknown>;
 if(Object.keys(p).length!==6||typeof p.presentation!=='string'||!['sheet','replace','page'].includes(p.presentation)||p.presentation==='sheet'&&(!input.list||input.current)||p.presentation==='replace'&&!input.current)throw Error('invalid trace presentation');
 for(const name of ['keep_filter','keep_tree_scroll','keep_detail_scroll','restore_focus','reveal_tabs'])if(typeof p[name]!=='boolean')throw Error('invalid trace retention');
 return value as TraceNavigationPlan;
}
