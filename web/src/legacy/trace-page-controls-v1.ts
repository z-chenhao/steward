import type {TracePageInput,TracePagePlan} from '../trace-page-controls-contract';
// Original1.0.1 and never-managed/direct readers only; delete with consumers.
export function legacyTracePageControls(input:TracePageInput):TracePagePlan {
 const state={...input.state};let open=input.open;
 if(input.list){const compact=input.width<=760;if(compact!==state.compact){if(compact){state.wide_open=open;open=false}else open=state.wide_open;state.compact=compact}}
 return {open,state,kinds:input.list?['select','trace','identifier','context']:['select','identifier','context'],row:input.list,focus_row:true};
}

// Original1.0.1 / explicitly never-managed navigation consumers only.
export function legacyTraceNavigation(input:import('../trace-page-controls-contract').TraceNavigationInput):import('../trace-page-controls-contract').TraceNavigationPlan {
 return {presentation:input.current?'replace':'sheet',keep_filter:input.same_trace,keep_tree_scroll:input.same_trace,keep_detail_scroll:input.same_selection,restore_focus:true,reveal_tabs:true};
}
