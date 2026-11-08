package sqlite

// A read projection of existing authoritative facts, plus optional dispatch
// evidence. The common columns are id,parent,type,name,run,key,step,status,
// started,ended,timing,detail. Only immutable admission timestamps order pages.
var observationSources = []string{
	`SELECT 'agent:'||r.run_id id,coalesce('agent:'||r.parent_run_id,'') parent,'agent' type,
 CASE WHEN r.kind='main' THEN 'Main' WHEN r.worker_position='root' THEN 'Root Worker' ELSE 'Child Worker' END name,
 r.run_id run,r.run_id key,NULL step,r.status status,r.created_at started,r.ended_at ended,'lifecycle' timing,coalesce(r.wait_reason,'') detail FROM members r`,
	`SELECT 'generation:'||g.generation_id,coalesce('tool:'||owner.invocation_id,'agent:'||r.run_id),'generation',
 CASE WHEN json_extract(c.provenance_json,'$.stage')='summary_request' THEN 'Summarize context'
 WHEN json_extract(g.request_json,'$.tool_generation.kind')='hosted_search' OR json_type(g.request_json,'$.web_search')='object' THEN 'Hosted web search'
 WHEN json_extract(g.request_json,'$.tool_generation.kind')='summary' THEN 'Summarize search results'
 WHEN json_extract(g.request_json,'$.tool_generation.kind')='page_answer' THEN 'Answer from page'
 WHEN json_type(g.request_json,'$.tool_generation')='object' THEN 'Tool model call' ELSE 'Generate response' END,
 r.run_id,g.generation_id,g.step_index,g.status,g.created_at,g.completed_at,'admission to acceptance',
 CASE WHEN coalesce(g.error_code,'')!='' THEN g.error_code WHEN owner.invocation_id IS NOT NULL THEN 'Internal model call for the parent tool; not an Agent loop turn.' ELSE '' END
 FROM model_generation g JOIN members r USING(run_id)
 LEFT JOIN context_compaction c ON c.run_id=g.run_id AND c.step_index=g.step_index
 LEFT JOIN tool_invocation owner ON owner.run_id=g.run_id AND owner.invocation_id=coalesce(json_extract(g.request_json,'$.tool_generation.invocation_id'),json_extract(g.request_json,'$.web_search.invocation_id'))`,
	`SELECT 'tool:'||i.invocation_id,coalesce('tool:'||i.parent_invocation_id,'agent:'||r.run_id),'tool',i.tool_name,r.run_id,i.invocation_id,g.step_index,i.status,i.created_at,
 CASE WHEN i.status IN ('succeeded','failed','cancelled','unknown') THEN coalesce(i.result_ready_at,i.updated_at) END,
 'tool lifecycle',coalesce(i.wait_reason||' · ','')||'Host execution starts: '||i.attempt_count FROM tool_invocation i JOIN members r USING(run_id) JOIN model_generation g ON g.generation_id=i.provider_exchange_id`,
	`SELECT 'approval:'||a.approval_id,'tool:'||i.invocation_id,'span','Wait for approval',r.run_id,a.approval_id,g.step_index,a.status,a.created_at,a.decided_at,'approval wait',''
 FROM approval a JOIN tool_invocation i USING(invocation_id) JOIN members r USING(run_id) JOIN model_generation g ON g.generation_id=i.provider_exchange_id`,
	`SELECT 'spend:'||a.approval_id,'agent:'||r.run_id,'span','Wait for spend approval',r.run_id,a.approval_id,NULL,a.status,a.created_at,a.decided_at,'approval wait','Frozen monetary authorization'
 FROM spend_approval a JOIN members r ON r.run_id=a.root_run_id`,
	`SELECT 'execution:'||r.run_id||':'||t.sequence,'tool:'||i.invocation_id,'span','Execute tool',r.run_id,cast(t.sequence AS text),g.step_index,'observed',t.occurred_at,
 (SELECT min(e.occurred_at) FROM run_trace e WHERE e.run_id=t.run_id AND e.sequence>t.sequence AND json_extract(e.event_json,'$.invocation_id')=i.invocation_id AND json_extract(e.event_json,'$.kind')='tool_execution_end'
 AND NOT EXISTS(SELECT 1 FROM run_trace n WHERE n.run_id=t.run_id AND n.sequence>t.sequence AND n.sequence<e.sequence AND json_extract(n.event_json,'$.invocation_id')=i.invocation_id AND json_extract(n.event_json,'$.kind')='tool_execution_start')),
 'observed execution segment','Best-effort timing; input/result belongs to the parent Tool'
 FROM run_trace t JOIN members r USING(run_id) JOIN tool_invocation i ON i.run_id=r.run_id AND i.invocation_id=json_extract(t.event_json,'$.invocation_id') JOIN model_generation g ON g.generation_id=i.provider_exchange_id
 WHERE json_extract(t.event_json,'$.kind')='tool_execution_start'`,

	`SELECT 'recall:'||r.run_id||':'||c.step_index,'agent:'||r.run_id,'retriever','Retrieve context',r.run_id,cast(c.step_index AS text),c.step_index,'saved',c.created_at,NULL,'saved point','Start time not recorded'
 FROM run_recall c JOIN members r USING(run_id)`,
	`SELECT 'context:'||r.run_id||':'||c.step_index,'agent:'||r.run_id,'chain','Prepare context',r.run_id,cast(c.step_index AS text),c.step_index,'saved',c.created_at,NULL,'saved point',coalesce(json_extract(c.provenance_json,'$.stage'),'unknown')
 FROM context_compaction c JOIN members r USING(run_id)`,
	`SELECT 'dispatch:'||d.attempt,'generation:'||d.generation_id,'span','Provider HTTP',r.run_id,d.attempt,g.step_index,
 CASE WHEN e.phase='error' THEN 'failed' WHEN e.http_status>=300 THEN 'http_error' WHEN e.phase='headers' THEN 'headers_received' WHEN g.status!='pending' OR r.status IN ('completed','failed','cancelled') THEN 'unknown' ELSE 'in_flight' END,
 d.occurred_at,e.occurred_at,'HTTP to headers',CASE WHEN e.phase='error' THEN e.error_code WHEN e.http_status>0 THEN 'HTTP '||e.http_status ELSE 'Outcome not recorded' END
 FROM dispatch_observation d JOIN model_generation g USING(generation_id) JOIN members r USING(run_id)
 LEFT JOIN dispatch_observation e ON e.generation_id=d.generation_id AND e.attempt=d.attempt AND e.phase IN ('headers','error') WHERE d.phase='started'`,
	`SELECT 'event:'||r.run_id||':'||t.sequence,
 CASE WHEN json_extract(t.event_json,'$.kind')='generation_reused' AND g.generation_id IS NOT NULL THEN 'generation:'||g.generation_id ELSE 'agent:'||r.run_id END,
 CASE WHEN json_extract(t.event_json,'$.kind')='agent_wait' THEN 'span' ELSE 'event' END,
 CASE json_extract(t.event_json,'$.kind') WHEN 'generation_reused' THEN 'Reuse accepted result' WHEN 'agent_start' THEN 'Enter / resume Agent' WHEN 'agent_wait' THEN 'Wait: '||coalesce(json_extract(t.event_json,'$.text'),'unknown') ELSE 'Agent finished' END,
 r.run_id,cast(t.sequence AS text),json_extract(t.event_json,'$.step_index'),
 CASE WHEN json_extract(t.event_json,'$.kind')='generation_reused' THEN 'reused' WHEN json_extract(t.event_json,'$.kind')='agent_wait' THEN 'wait_observed' ELSE 'recorded' END,
 t.occurred_at,
 CASE WHEN json_extract(t.event_json,'$.kind')='agent_wait' THEN (SELECT min(n.occurred_at) FROM run_trace n WHERE n.run_id=r.run_id AND n.sequence>t.sequence AND json_extract(n.event_json,'$.kind')='agent_start') END,
 CASE WHEN json_extract(t.event_json,'$.kind')='agent_wait' THEN 'observed wait' ELSE 'event' END,'Best-effort event; missing intervals remain unknown'
 FROM run_trace t JOIN members r USING(run_id) LEFT JOIN model_generation g ON g.run_id=r.run_id AND g.step_index=json_extract(t.event_json,'$.step_index')
 WHERE json_extract(t.event_json,'$.kind') IN ('generation_reused','agent_start','agent_end','agent_wait')`,
}
