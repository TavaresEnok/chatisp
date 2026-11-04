import { useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";

export type AudioCapture = {
  blob: Blob;
  mimeType: string;
  replyToMessageId?: string;
};

type Phase = "idle" | "recording" | "ready" | "sending";

const internalId = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function supportedMime(): string {
  if (typeof MediaRecorder === "undefined" || typeof MediaRecorder.isTypeSupported !== "function") return "";
  if (MediaRecorder.isTypeSupported("audio/ogg;codecs=opus")) return "audio/ogg;codecs=opus";
  if (MediaRecorder.isTypeSupported("audio/webm")) return "audio/webm";
  return "";
}

export function AudioRecorder({
  disabled = false,
  onSend,
}: {
  disabled?: boolean;
  onSend?: (capture: AudioCapture) => Promise<void> | void;
}) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [notice, setNotice] = useState("");
  const [reply, setReply] = useState("");
  const recorder = useRef<MediaRecorder | null>(null);
  const stream = useRef<MediaStream | null>(null);
  const chunks = useRef<Blob[]>([]);
  const blob = useRef<Blob | null>(null);

  function stopTracks() {
    stream.current?.getTracks().forEach((track) => track.stop());
    stream.current = null;
  }

  async function start() {
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
      setNotice("Este navegador não grava áudio.");
      return;
    }
    try {
      const live = await navigator.mediaDevices.getUserMedia({ audio: true });
      stream.current = live;
      const mime = supportedMime();
      const next = mime ? new MediaRecorder(live, { mimeType: mime }) : new MediaRecorder(live);
      chunks.current = [];
      next.ondataavailable = (event) => {
        if (event.data.size > 0) chunks.current.push(event.data);
      };
      next.onstop = () => {
        const type = next.mimeType || chunks.current[0]?.type || "application/octet-stream";
        const original = new Blob(chunks.current, { type });
        blob.current = original;
        stopTracks();
        setPhase("ready");
        setNotice(`Áudio original pronto (${type}, ${original.size} bytes). O painel envia este blob sem converter.`);
      };
      recorder.current = next;
      next.start();
      setPhase("recording");
      setNotice("Gravando áudio neste navegador.");
    } catch {
      stopTracks();
      setPhase("idle");
      setNotice("Não foi possível acessar o microfone.");
    }
  }

  function stop() {
    if (recorder.current && recorder.current.state !== "inactive") recorder.current.stop();
  }

  function discard() {
    blob.current = null;
    chunks.current = [];
    setPhase("idle");
    setNotice("Gravação descartada. Nenhum áudio foi convertido.");
  }

  async function send() {
    const original = blob.current;
    if (!original || !onSend || phase === "sending") return;
    const citation = reply.trim();
    if (citation !== "" && !internalId.test(citation)) {
      setNotice("Informe o ID interno da mensagem citada.");
      return;
    }
    setPhase("sending");
    try {
      await onSend({
        blob: original,
        mimeType: original.type,
        replyToMessageId: citation || undefined,
      });
      blob.current = null;
      setReply("");
      setPhase("idle");
      setNotice("Áudio original enviado. O navegador não converteu o arquivo.");
    } catch (error) {
      setPhase("ready");
      setNotice(error instanceof Error && error.message ? error.message : "O envio do áudio original falhou.");
    }
  }

  const canRecord = !disabled && phase !== "recording" && phase !== "sending";
  return (
    <div className="flex flex-col gap-2" aria-label="Gravação de áudio">
      <div className="flex flex-wrap gap-2">
        {phase === "recording" ? (
          <Button type="button" variant="outline" className="h-11 w-fit px-3" onClick={stop}>
            Parar gravação
          </Button>
        ) : (
          <Button type="button" variant="outline" className="h-11 w-fit px-3" disabled={!canRecord} onClick={() => void start()}>
            Gravar áudio
          </Button>
        )}
        {phase === "ready" || phase === "sending" ? (
          <>
            <Button type="button" className="h-11 w-fit px-3" disabled={disabled || !onSend || phase === "sending"} onClick={() => void send()}>
              Enviar áudio original
            </Button>
            <Button type="button" variant="outline" className="h-11 w-fit px-3" disabled={phase === "sending"} onClick={discard}>
              Descartar áudio
            </Button>
          </>
        ) : null}
      </div>
      <div className="flex flex-col gap-1">
        <Label htmlFor="citacao-mensagem">Responder à mensagem</Label>
        <input
          id="citacao-mensagem"
          value={reply}
          autoComplete="off"
          placeholder="ID interno da mensagem"
          className="h-11 w-full rounded-lg border border-input bg-transparent px-2.5 text-sm"
          onChange={(event) => setReply(event.target.value)}
        />
      </div>
      {notice ? (
        <p role="status" className="text-sm text-foreground">
          {notice}
        </p>
      ) : null}
    </div>
  );
}
