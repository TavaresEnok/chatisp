import { useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { Link } from "react-router";
import { beginMfa, confirmMfa } from "@/api/panel";
import type { Enrollment } from "@/api/panel";
import { errorMessage } from "@/api/problems";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useSessionStore } from "@/stores/session-store";

export function MfaEnrollment() {
  const principal = useSessionStore((state) => state.principal);
  const completeEnrollment = useSessionStore((state) => state.completeEnrollment);
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [code, setCode] = useState("");
  const [serverError, setServerError] = useState<string | null>(null);
  const [copyStatus, setCopyStatus] = useState("");
  const [pending, setPending] = useState(false);
  const [completed, setCompleted] = useState(false);

  if (!principal) {
    return <p>Entre para configurar a autenticação em duas etapas. <Link to="/entrar">Entrar</Link></p>;
  }
  if (!completed && principal.mfaEnabled === true) {
    return <div className="flex flex-col gap-3">
      <p>A autenticação em duas etapas está ativa nesta conta.</p>
      <Link to="/configuracoes">Voltar às configurações</Link>
    </div>;
  }
  if (!completed && principal.mfaEnabled === undefined && !principal.mfaEnrollmentRequired) {
    return <div className="flex flex-col gap-3">
      <p>Não foi possível verificar o estado da autenticação em duas etapas. Atualize a página e tente novamente.</p>
      <Link to="/configuracoes">Voltar às configurações</Link>
    </div>;
  }

  function startEnrollment() {
    setPending(true);
    setServerError(null);
    void beginMfa()
      .then(setEnrollment)
      .catch((error: unknown) => setServerError(errorMessage(error)))
      .finally(() => setPending(false));
  }

  async function copySecret(secret: string) {
    try {
      await navigator.clipboard.writeText(secret);
      setCopyStatus("Chave copiada.");
    } catch {
      setCopyStatus("Não foi possível copiar. Selecione a chave manualmente.");
    }
  }

  function submitCode(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setServerError(null);
    if (!code.trim()) {
      setServerError("Informe o código do aplicativo.");
      return;
    }
    setPending(true);
    void confirmMfa(code.trim()).then((state) => {
      if (state.mfa_enrollment_required) {
        setServerError("O cadastro ainda não foi confirmado.");
        return;
      }
      completeEnrollment();
      setCompleted(true);
      setCode("");
    }).catch((error: unknown) => setServerError(errorMessage(error)))
      .finally(() => setPending(false));
  }

  return (
    <div className="flex flex-col gap-4">
      {completed ? (
        <p role="status">Autenticação em duas etapas ativada. Guarde os códigos de recuperação antes de sair desta página.</p>
      ) : (
        <p>A autenticação em duas etapas é opcional. Ao ativá-la, você usará um código do aplicativo para entrar.</p>
      )}
      {serverError && <p role="alert" className="text-sm text-destructive">{serverError}</p>}
      {!enrollment && !completed && (
        <Button type="button" disabled={pending} onClick={startEnrollment}>Iniciar cadastro</Button>
      )}
      {enrollment && (
        <div className="flex flex-col gap-4">
          {!completed && (
            <>
              <p>1. Abra seu aplicativo autenticador e escolha adicionar uma conta. Escaneie o QR code:</p>
              <div className="w-fit rounded bg-white p-3">
                <QRCodeSVG value={enrollment.otpauth_url} size={200} marginSize={2} role="img" aria-label="QR code para aplicativo autenticador" />
              </div>
              <details>
                <summary className="cursor-pointer underline">Não consigo escanear o QR Code</summary>
                <div className="flex flex-col gap-2 pt-2">
                  <p>Informe esta chave manualmente no aplicativo:</p>
                  <p className="break-all font-mono">Segredo: <span>{enrollment.secret}</span></p>
                  <Button type="button" variant="outline" onClick={() => void copySecret(enrollment.secret)}>Copiar chave</Button>
                  {copyStatus && <p role="status">{copyStatus}</p>}
                </div>
              </details>
            </>
          )}
          <div className="flex flex-col gap-2">
            <p>{completed ? "Códigos de recuperação" : "2. Guarde estes códigos de recuperação em um lugar seguro. Cada código funciona uma vez; eles aparecem somente agora."}</p>
            <ul aria-label="Códigos de recuperação" className="font-mono">
              {(enrollment.recovery_codes ?? []).map((item) => <li key={item}>{item}</li>)}
            </ul>
          </div>
          {!completed && (
            <form className="flex flex-col gap-2" onSubmit={submitCode}>
              <p>3. Informe o código de seis dígitos mostrado no aplicativo para concluir.</p>
              <Label htmlFor="codigo-do-aplicativo">Código do aplicativo</Label>
              <Input id="codigo-do-aplicativo" inputMode="numeric" autoComplete="one-time-code" maxLength={16} value={code} onChange={(event) => setCode(event.target.value)} />
              <Button type="submit" disabled={pending}>Confirmar código</Button>
            </form>
          )}
        </div>
      )}
      <Link to={principal.mfaEnrollmentRequired ? "/entrar" : "/configuracoes"} className="underline">
        {completed ? "Concluir e voltar às configurações" : "Voltar"}
      </Link>
    </div>
  );
}
