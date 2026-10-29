import { AuthScreen } from "@/components/auth/auth-screen";
import { MfaEnrollment } from "@/components/auth/mfa-enrollment";

export function MfaPage() {
  return (
    <AuthScreen title="Autenticação em duas etapas">
      <MfaEnrollment />
    </AuthScreen>
  );
}
