import {useEffect,useState} from 'react';
import {readFormView,type FormView} from './host-api';
import {NativeFormControls,type FormControlProps} from './extensions';

// Host-bound submission stays outside the frozen display. Missing/corrupt
// managed data never falls through to the historical native policy.
export function FrozenFormView({reference,fallback,...controls}:Omit<FormControlProps,'props'|'defaults'>&{fallback:string}){
 const[view,setView]=useState<FormView|null>(null);
 useEffect(()=>{
  const request=new AbortController();let alive=true;setView(null);
  void readFormView(reference,request.signal).then(value=>{if(alive)setView(value)}).catch(()=>{if(alive)setView(null)});
  return()=>{alive=false;request.abort()};
 },[reference]);
 if(!view)return <p className="semantic">{fallback}</p>;
 return <NativeFormControls key={reference} {...controls} reference={reference} props={view.props} defaults={view.defaults}/>;
}
