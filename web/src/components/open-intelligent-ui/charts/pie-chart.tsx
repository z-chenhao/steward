// Adapted from OpenIntelligentUI @ 8391a94; MIT, web/licenses/OpenIntelligentUI.txt.
import {z} from 'zod';
import {LegacyChartExplorer} from '../../../intelligent-ui/legacy/chart-explorer-v1';
export const PieChartProps=z.object({title:z.string(),description:z.string(),data:z.array(z.object({label:z.string(),value:z.number()}))});
export function PieChart(props:z.infer<typeof PieChartProps>&{reference?:string}){return <LegacyChartExplorer kind="pie" {...props}/>}
