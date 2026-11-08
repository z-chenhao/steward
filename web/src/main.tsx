import {enhanceFilterRails} from './components/ui/filter-rail';
import {enhanceCopyControls} from './components/ui/clipboard';
import {enhanceFilterForms} from './components/ui/filter-form';
import {enhanceTimeDetails} from './components/ui/time-detail';
import {enhanceLocalTimes} from './local-time';
import {enhanceFormMenus} from './components/ui/form-menu';
import {enhanceTaskFilters} from './task-filters';
import {enhanceContextHelp} from './components/ui/help';
import {enhanceWorkers} from './workers';
import {enhanceAppConnections} from './app-connections';
import {enhanceComputerView} from './computer';
import {enhanceShell} from './shell';
import {enhanceBrandMotion} from './brand';
import {enhanceSettings} from './settings';
import {enhanceFormDisclosures} from './components/ui/disclosure';
import {enhanceSelectControls} from './components/ui/select';
import {enhanceCapabilityForms} from './capability-forms';
import {enhanceObservability} from './observability';
import {enhanceMemory} from './memory';
import {enhanceComposer} from './composer';
import {enhanceComposerModels} from './composer-models';
import {enhanceProjections} from './projections';
import {startLiveUpdates} from './live-updates';
import {enhanceRichForms} from './rich-forms';

// Capture server markup before enhancement, so unchanged React islands remain
// mounted. The composer has a separate lifetime from every live region.
enhanceCopyControls();
enhanceShell();
enhanceComputerView();
enhanceWorkers();
const live=startLiveUpdates();
enhanceLocalTimes();
enhanceTimeDetails();
enhanceProjections();
enhanceRichForms();
enhanceComposer(live?.messageAccepted);
enhanceComposerModels();
live?.start();

enhanceObservability();
enhanceMemory();
enhanceFilterRails();

enhanceAppConnections();
enhanceContextHelp();
enhanceSettings();
enhanceFormMenus();
enhanceTaskFilters();
enhanceFormDisclosures();
enhanceSelectControls();
enhanceFilterForms();
enhanceCapabilityForms();

enhanceBrandMotion();
