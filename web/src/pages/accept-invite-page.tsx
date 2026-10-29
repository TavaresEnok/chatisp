import { AcceptInviteForm } from "@/components/auth/accept-invite-form";
import { AuthScreen } from "@/components/auth/auth-screen";

export function AcceptInvitePage() {
  return (
    <AuthScreen title="Aceitar convite">
      <AcceptInviteForm />
    </AuthScreen>
  );
}
