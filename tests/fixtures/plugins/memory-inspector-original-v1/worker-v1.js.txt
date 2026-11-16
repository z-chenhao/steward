'use strict';
// Feature-owned pure UI decisions. Inputs contain booleans/indexes only: no
// bodies, identifiers, URLs, DOM, credentials, storage or effect capabilities.
(()=>{
 const bool=v=>typeof v==='boolean';
 const integer=(v,min,max)=>Number.isSafeInteger(v)&&v>=min&&v<=max;
 function project(r){
  switch(r.type){
   case 'init':
    if(!bool(r.rail_present)||!bool(r.rail_open))throw Error('invalid init');
    return {type:'ready',compact_width:760,sheet_modal:false,storage_key:'steward.memory.peek-width.v1',labels:{details:'Memory details',previous:'Previous memory',next:'Next memory',resize_sources:'Resize memory sources'},wide_open:r.rail_present?r.rail_open:true};
   case 'fit':{
    if(!['rail_present','rail_open','compact','wide_open'].every(k=>bool(r[k])))throw Error('invalid fit');
    let open=r.rail_open,wide=r.wide_open;
    if(!r.rail_present)return {type:'fit',rail_open:null,wide_open:wide};
    if(r.compact){wide=open;open=false}else open=wide;
    return {type:'fit',rail_open:open,wide_open:wide};
   }
   case 'click':{
    if(!['list_page','primary','row','blocked','selection','direct_allowed','row_allowed'].every(k=>bool(r[k]))||!['none','open','select'].includes(r.anchor_kind))throw Error('invalid click');
    if(!r.primary)return {type:'click',intercept:false,focus_row:false,target:'none'};
    const direct=r.anchor_kind==='select'||r.list_page&&r.anchor_kind==='open';
    const row=r.list_page&&!direct&&!r.blocked&&!r.selection&&r.row;
    const target=direct&&r.direct_allowed?'direct':row&&r.row_allowed?'row':'none';
    return {type:'click',intercept:target!=='none',focus_row:row,target};
   }
   case 'commit':{
    if(!['list_page','same_record','has_focused','push'].every(k=>bool(r[k]))||!integer(r.count,0,4096)||!integer(r.index,-1,Math.max(-1,r.count-1))||r.depth!==null&&!integer(r.depth,0,4096))throw Error('invalid commit');
    return {type:'commit',previous_hidden:false,next_hidden:false,previous:r.index>0?r.index-1:null,next:r.index>=0&&r.index+1<r.count?r.index+1:null,previous_disabled:r.index<=0,next_disabled:r.index<0||r.index>=r.count-1,restore_source:r.same_record,restore_focus:r.has_focused,history_push:r.push,history_depth:r.push&&r.list_page?(r.depth??0)+1:null};
   }
   case 'history':{
    if(!['close','popstate','pageshow'].includes(r.kind)||!bool(r.list_page)||!bool(r.has_ref)||!bool(r.persisted)||r.depth!==null&&!integer(r.depth,0,4096))throw Error('invalid history');
    let mode='none',steps=0;
    if(r.kind==='close'){if(r.depth!==null&&r.depth>0){mode='back';steps=r.depth}else mode='dismiss'}
    else if(r.kind==='popstate'){if(r.list_page&&!r.depth)mode='dismiss';else if(r.has_ref)mode='select'}
    else if(r.persisted&&r.list_page&&r.depth)mode='select';
    return {type:'history',mode,steps};
   }
  }
  throw Error('unknown request');
 }
 self.onmessage=event=>{const r=event.data;try{if(!r||typeof r!=='object'||Array.isArray(r)||!integer(r.seq,1,Number.MAX_SAFE_INTEGER))throw Error('invalid envelope');self.postMessage({...project(r),seq:r.seq})}catch{self.postMessage({type:'failed',seq:integer(r?.seq,1,Number.MAX_SAFE_INTEGER)?r.seq:0})}};
})();
