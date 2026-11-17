import {ActionControls} from '../action-controls';
import {type ActionView} from '../action-view';

// Only original packages before UI1.27 use this frozen feedback policy. Remove
// after their published references and in-flight actions retire; managed failure
// never selects it. The gesture/CAS/receipt driver is shared, not duplicated.
const original:ActionView={component:0,working_label:'Working',unconfirmed_error:'Action not confirmed. Retry.',unknown_error:'Result unknown. Check the conversation.',cancelled_error:'Action cancelled.',not_executed_error:'Action was not completed.',failed_error:'Action failed. Retry.',success_indicator:true};
export function LegacyToolActionButton({label,reference}:{label:string;reference:string}){return <ActionControls label={label} reference={reference} view={original}/>}
