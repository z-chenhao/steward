// Private DOM bindings for the owning Models resource. Values are supplied by
// its sealed view; no provider names, default endpoints or credentials live here.
export function enhanceModelConnectionControls(api:HTMLFormElement){
 const provider=api.querySelector<HTMLSelectElement>('[name=service]')!,key=api.querySelector<HTMLInputElement>('[name=api_key]')!,endpoint=api.querySelector<HTMLInputElement>('[name=endpoint]')!,advanced=api.querySelector<HTMLDetailsElement>('.model-connection-advanced')!;
 const selected=()=>provider.selectedOptions[0].dataset;
 const sync=()=>{const policy=selected();key.required=policy.modelOwnerKeyRequired==='true';endpoint.readOnly=policy.modelOwnerEndpointReadonly==='true';if(policy.modelOwnerExpandAdvanced==='true')advanced.open=true};
 sync();provider.addEventListener('change',()=>{sync();const policy=selected();if(policy.modelOwnerEndpointForce==='true'||endpoint.value===policy.modelOwnerEndpointReplace)endpoint.value=policy.modelOwnerEndpointDefault!});
}
