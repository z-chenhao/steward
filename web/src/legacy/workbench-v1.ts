// Published headerless updates v0 and explicit unbundled Web embeddings only.
import {t} from '../i18n';
export const legacyWorkbenchText=t;
export const legacyWorkbenchTiming={poll_ms:1000,max_poll_ms:30000,read_timeout_ms:10000};
export function legacyWorkbenchRegions(view:string):string[]|null {
 if(view==='/')return ['workspace-status','conversation-attention','conversation-messages','conversation-approvals'];
 if(view==='/tasks/attention')return ['workspace-status','tasks-attention'];
 if(view==='/tasks'||view==='/tasks/configurations')return ['workspace-status','ongoing-work'];
 if(view.startsWith('/tasks/executions/'))return ['workspace-status','execution-header','execution-progress','task-messages','execution-side'];
 if(view.startsWith('/tasks/'))return ['workspace-status','task-header','task-feedback','task-progress','task-decisions','task-messages','task-milestones'];
 if(view.startsWith('/settings/'))return ['workspace-status','signin-status'];
 return null;
}
