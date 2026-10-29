import { PasswordResetConfirmForm } from "@/components/auth/password-reset-forms";
import { AuthScreen } from "@/components/auth/auth-screen";

export function PasswordResetConfirmPage() {
  return (
    <AuthScreen title="Confirmar nova senha">
      <PasswordResetConfirmForm />
    </AuthScreen>
  );
}
