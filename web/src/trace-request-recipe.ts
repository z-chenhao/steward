// Private recipe for the existing provider-request reader. Bodies stay local.
export type TraceRequestRecipe={sections:('system'|'messages'|'tools'|'parameters')[];expanded:{system:boolean;messages:'last'|'all'|'none';tools:boolean;tool:boolean;parameters:boolean};textRules:{separator:string;skipEmpty:boolean};singleMessage:{roleField:string;role:string;contentField:string};uppercaseRole:boolean;capture:string[];body:string;omitted:string;model:string;instructions:string[];messages:string[];roles:string[];text:string;content:string[];tools:string;definition:string;name:string;exclude:string[];excerpt:number;labels:{indexPrefix:string;indexSuffix:string;roleSuffix:string;request:string;missing:string;system:string;message:string;tool:string;tools:string;parameters:string}};
export function acceptTraceRequestRecipe(value:unknown):TraceRequestRecipe {
 if(!value||typeof value!=='object'||Array.isArray(value))throw Error('invalid request recipe');
 const p=value as Record<string,unknown>,key=(v:unknown)=>typeof v==='string'&&/^[a-z_]{1,40}$/.test(v),keys=(v:unknown)=>Array.isArray(v)&&v.length>0&&v.length<=16&&v.every(key);
 for(const name of ['capture','instructions','messages','roles','content','exclude'])if(!keys(p[name]))throw Error('invalid request fields');
 for(const name of ['body','omitted','model','text','tools','definition','name'])if(!key(p[name]))throw Error('invalid request field');
 if(!Number.isSafeInteger(p.excerpt)||Number(p.excerpt)<0||Number(p.excerpt)>512||!p.labels||typeof p.labels!=='object'||Array.isArray(p.labels))throw Error('invalid request labels');
 const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
 if(!Array.isArray(p.sections)||p.sections.length!==4||new Set(p.sections).size!==4||p.sections.some(section=>!['system','messages','tools','parameters'].includes(section)))throw Error('invalid request sections');
 if(!object(p.expanded)||typeof p.expanded.messages!=='string'||!['last','all','none'].includes(p.expanded.messages)||['system','tools','tool','parameters'].some(name=>typeof (p.expanded as Record<string,unknown>)[name]!=='boolean'))throw Error('invalid request expansion');
 if(!object(p.textRules)||typeof p.textRules.separator!=='string'||p.textRules.separator.length>16||typeof p.textRules.skipEmpty!=='boolean')throw Error('invalid text rules');
 if(!object(p.singleMessage)||!key(p.singleMessage.roleField)||!key(p.singleMessage.contentField)||p.singleMessage.roleField===p.singleMessage.contentField||typeof p.singleMessage.role!=='string'||p.singleMessage.role.length>64||typeof p.uppercaseRole!=='boolean')throw Error('invalid single message');
 const labels=p.labels as Record<string,unknown>;for(const name of ['indexPrefix','indexSuffix','roleSuffix','request','missing','system','message','tool','tools','parameters'])if(typeof labels[name]!=='string'||labels[name].length>256)throw Error('invalid request label');
 return value as TraceRequestRecipe;
}
