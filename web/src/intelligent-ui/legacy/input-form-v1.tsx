import {NativeFormControls,type FormControlProps} from '../extensions';

// Historical Runs without the form-view service retain the original defaults.
// Remove with their form-v2 references/drafts once those consumers retire.
export function LegacyInputForm(value:Omit<FormControlProps,'defaults'>){
 const defaults=Object.fromEntries(value.props.fields.map(field=>[field.name,field.type==='multiselect'?'[]':field.type==='boolean'?'false':'']));
 return <NativeFormControls {...value} defaults={defaults}/>;
}
