// Adapted from CopilotKit/OpenIntelligentUI @ 8391a94c1c3939c679e40ddd7bfc08a9ef690e70.
// MIT; see web/licenses/OpenIntelligentUI.txt. Native rendering; no generated markup.
import { useEffect, useState } from "react";

export interface TimeSlot {
  date: string;
  time: string;
  duration?: string;
}

export interface MeetingTimePickerProps {
  status: "inProgress" | "executing" | "complete";
  respond?: (response: string) => Promise<void>;
  response?: string;
  disabled?: boolean;
  reasonForScheduling?: string;
  meetingDuration?: number;
  title?: string;
  timeSlots?: TimeSlot[];
}

export function LegacyMeetingTimePicker({
  status,
  respond,
  response,
  disabled = false,
  reasonForScheduling,
  meetingDuration,
  title = "Schedule a Meeting",
  timeSlots = [
    { date: "Tomorrow", time: "2:00 PM", duration: "30 min" },
    { date: "Friday", time: "10:00 AM", duration: "30 min" },
    { date: "Next Monday", time: "3:00 PM", duration: "30 min" },
  ]
}: MeetingTimePickerProps) {
  const displayTitle = reasonForScheduling || title;
  const slots = meetingDuration
    ? timeSlots.map((slot) => ({ ...slot, duration: `${meetingDuration} min` }))
    : timeSlots;
  const [selectedSlot, setSelectedSlot] = useState<TimeSlot | null>(null);
  const [declined, setDeclined] = useState(false);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const describe = (slot: TimeSlot) => `Meeting scheduled for ${slot.date} at ${slot.time}${slot.duration ? ` (${slot.duration})` : ''}.`;
  useEffect(() => { if (!response) return; const slot=response!=="decline"?slots[Number(response)]:undefined; if(slot)setSelectedSlot(slot); else setDeclined(true); }, [response]);
  const handleSelectSlot = async (slot: TimeSlot) => {
    if(busy || disabled)return;setBusy(true);setError("");
    try { await respond?.(describe(slot)); setSelectedSlot(slot); }
    catch { setError("Your selection was not confirmed. Retry the same time."); }
    finally { setBusy(false); }
  };

  const handleDecline = async () => {
    if(busy || disabled)return;setBusy(true);setError("");
    try { await respond?.("The user declined all proposed meeting times. Please suggest alternative times or ask for their availability."); setDeclined(true); }
    catch { setError("Your selection was not confirmed. Retry the same choice."); }
    finally { setBusy(false); }
  };

  return (
    <section className="native-time-picker">
      {selectedSlot ? <div className="native-time-confirmation"><h3 className="native-answer-title">Time Selected</h3><p>{selectedSlot.date} at {selectedSlot.time}{selectedSlot.duration && <span> · {selectedSlot.duration}</span>}</p></div>
      : declined ? <div className="native-time-confirmation"><h3 className="native-answer-title">No Time Selected</h3></div>
      : <><div className="native-answer-heading"><h3 className="native-answer-title">{displayTitle}</h3></div>
        {status === "executing" && <fieldset disabled={busy||disabled} className="native-time-options">
          {slots.map((slot,index)=><button type="button" key={index} className="native-time-option" onClick={()=>handleSelectSlot(slot)}><span className="native-time-label"><strong>{slot.date}</strong><span>{slot.time}</span></span>{slot.duration&&<span className="native-time-duration">{slot.duration}</span>}</button>)}
          <button type="button" className="native-time-decline" onClick={handleDecline}>None of these work</button>
        </fieldset>}
      </>}
      {busy&&<p className="native-answer-description" role="status">Saving selection…</p>}
      {error&&<p className="field-help" role="alert">{error}</p>}
    </section>
  );
}
