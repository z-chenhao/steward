// Only Host-authored data-model-legacy-v1 forms, for never-activated historical
// installations/embeddings. Remove with that explicit template boundary.
export function enhanceLegacyModelConnectionV1(api:HTMLFormElement){
 const provider=api.querySelector<HTMLSelectElement>('[name=service]')!,key=api.querySelector<HTMLInputElement>('[name=api_key]')!,endpoint=api.querySelector<HTMLInputElement>('[name=endpoint]')!,advanced=api.querySelector<HTMLDetailsElement>('.model-connection-advanced')!;
 const sync=()=>{key.required=provider.value==='openai';endpoint.readOnly=provider.value==='openai';if(provider.value==='custom')advanced.open=true};
 sync();provider.addEventListener('change',()=>{sync();if(provider.value==='custom'&&endpoint.value==='https://api.openai.com/v1')endpoint.value='http://127.0.0.1:8000/v1';if(provider.value==='openai')endpoint.value='https://api.openai.com/v1'});
}
