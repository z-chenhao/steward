import {t} from '../i18n';
import type {TraceRequestRecipe} from '../trace-request-recipe';

// Retained for original 1.0.1 and explicitly unmanaged readers.
export function legacyTraceRequestRecipe():TraceRequestRecipe{
 return {
  sections:['system','messages','tools','parameters'],
  expanded:{system:false,messages:'last',tools:false,tool:false,parameters:false},
  textRules:{separator:'\n',skipEmpty:true},
  singleMessage:{roleField:'role',role:'user',contentField:'content'},
  uppercaseRole:true,capture:['attempt','body'],body:'body',omitted:'omitted',model:'model',
  instructions:['instructions','system'],messages:['messages','input','contents'],roles:['role','type'],
  text:'text',content:['content','parts'],tools:'tools',definition:'function',name:'name',
  exclude:['instructions','system','messages','input','contents','tools'],excerpt:90,
  labels:{indexPrefix:'[',indexSuffix:'] ',roleSuffix:' · ',request:t('Request'),missing:t('Request body was not captured.'),
   system:t('System instructions'),message:t('Message'),tool:t('Tool'),tools:t('Tools'),parameters:t('Parameters')},
 };
}
