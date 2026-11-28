import { OctagonXIcon, SendIcon } from "lucide-react";
import { useState } from "react";
import { useAbort, useSteer } from "@/api/mutations";
import { Button } from "@/components/ui/button";
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/toast";
import { stepName, type Step } from "@/steps";

// Intervene steers or aborts the running agent step (D52): a steer reaches
// the agent at its next model call without stopping it; an abort stops the
// session and fails the run with the person's reason.
export function Intervene({ step }: { step: Step }) {
  const [text, setText] = useState("");
  const m = useSteer();
  const name = stepName(step);
  return (
    <div className="border-t border-line bg-muted px-4 py-3">
      <label htmlFor="steer" className="text-xs font-medium text-muted-fg">
        Steer {step.role ? `the ${step.role}` : name}: the message reaches the session at its next model call, without stopping it
      </label>
      <Textarea id="steer" rows={2} maxLength={8000} className="mt-1.5" placeholder="Check the error text against the failing test before you push"
        value={text} disabled={m.isPending} onChange={(e) => { setText(e.target.value); m.reset(); }} />
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <Button disabled={m.isPending || text.trim() === ""}
          onClick={() => m.mutate({ stepID: step.id, text }, { onSuccess: () => { setText(""); toast(`Steer sent to ${name}.`); } })}>
          <SendIcon className="size-3.5" />Steer
        </Button>
        <AbortStep step={step} />
        {m.isSuccess && <span className="text-xs text-muted-fg">Sent; the agent gets it at its next model call.</span>}
      </div>
      {m.error && <p role="alert" className="mt-2 text-xs text-bad">Could not steer: {m.error.message}</p>}
    </div>
  );
}

function AbortStep({ step }: { step: Step }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const m = useAbort();
  const name = stepName(step);
  return (
    <AlertDialog open={open} onOpenChange={(o) => { setOpen(o); if (!o) m.reset(); }}>
      <AlertDialogTrigger asChild>
        <Button variant="destructive"><OctagonXIcon className="size-3.5" />Abort step</Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Abort {name}?</AlertDialogTitle>
          <AlertDialogDescription>
            The session stops and pushes nothing, and the run fails with your reason. The issue can be run again.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="grid gap-1.5">
          <label htmlFor="abort-reason" className="text-xs font-medium text-muted-fg">Reason (required; it goes in the run's failure)</label>
          <Textarea id="abort-reason" rows={3} maxLength={4000} placeholder="Wrong approach: register the PNG decoder instead"
            value={reason} disabled={m.isPending} onChange={(e) => { setReason(e.target.value); m.reset(); }} />
          {m.error && <p role="alert" className="text-xs text-bad">Could not abort: {m.error.message}</p>}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep running</AlertDialogCancel>
          <Button variant="destructive" disabled={m.isPending || reason.trim() === ""}
            onClick={() => m.mutate({ stepID: step.id, reason }, { onSuccess: () => { setOpen(false); setReason(""); toast(`Abort sent to ${name}.`); } })}>
            <OctagonXIcon className="size-3.5" />Abort step
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
