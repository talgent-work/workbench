import { useEffect, useState } from "react";
import { Alert, Button, Spinner } from "@heroui/react";
import { useSearchParams } from "react-router-dom";
import github from "../assets/brand-icons/github.svg";
import { safeInternalPath, signInHref } from "../lib/session";
import { LoginThreads } from "./login-threads";
import "./login-page.css";

const PRODUCT_SITE = "https://talgent.work";

// Why Account sent the person back here instead of into the workspace: one
// notice per failure code Account's /auth handler redirects with
// (internal/business/account/transport/http/handler.go); change both.
const FAILURES: Record<string, { title: string; body: string }> = {
  denied: { title: "GitHub sign-in was cancelled", body: "You left GitHub without authorizing Talgent. Nothing was changed. Try again when you are ready." },
  expired: { title: "This sign-in attempt expired", body: "The link back from GitHub was older than ten minutes or opened in another browser. Start again from here." },
  email: { title: "GitHub did not share an email address", body: "Talgent needs a verified email on your GitHub account. Add one at github.com/settings/emails, then try again." },
  unavailable: { title: "GitHub sign-in didn’t finish", body: "Talgent couldn’t complete the sign-in with GitHub. Try again in a moment." },
};

// The approved brand lockup from the sidebar.
function Brand() {
  return (
    <div className="p-brand-lockup" role="img" aria-label="Talgent">
      <span className="p-brand-icon" aria-hidden="true" />
      <span className="p-brand-name" aria-hidden="true" />
    </div>
  );
}

// The Marketplace GitHub mark, tinted through a mask so it reads white on the
// black primary button.
function GitHubMark() {
  return <span aria-hidden="true" className="si-github-mark" style={{ maskImage: `url("${github}")`, WebkitMaskImage: `url("${github}")` }} />;
}

function FailureNotice({ code }: { code: string | null }) {
  const failure = code ? FAILURES[code] : undefined;
  if (!failure) return null;
  return (
    <Alert status="danger" role="alert">
      <Alert.Indicator />
      <Alert.Content>
        <Alert.Title>{failure.title}</Alert.Title>
        <Alert.Description>{failure.body}</Alert.Description>
      </Alert.Content>
    </Alert>
  );
}


// Signing in happens on GitHub; this page says where the person is before
// they leave, and why they came back if it did not work. Telling people what
// Talgent is belongs to the product site, which it links to.
export function LoginPage() {
  const [searchParams] = useSearchParams();
  const [leaving, setLeaving] = useState(false);
  const next = safeInternalPath(searchParams.get("next"));
  // Coming back from GitHub with the browser's Back button restores this page
  // from cache; the button must be usable again.
  useEffect(() => {
    const restore = (event: PageTransitionEvent) => { if (event.persisted) setLeaving(false); };
    window.addEventListener("pageshow", restore);
    return () => window.removeEventListener("pageshow", restore);
  }, []);
  const continueWithGitHub = () => {
    setLeaving(true);
    window.location.assign(signInHref(next));
  };
  return (
    <main className="si-page grid min-h-dvh place-items-center p-6">
      <LoginThreads />
      <div className="grid w-full max-w-[22rem] justify-items-center gap-10">
        <div className="si-brand my-3"><Brand /></div>
        {/* The button says it all on screen; the heading stays for assistive tech. */}
        <h1 className="sr-only">Sign in to Talgent</h1>
        <div className="grid w-full gap-4">
          <FailureNotice code={searchParams.get("error")} />
          <Button variant="primary" className="si-github-button" isPending={leaving} onPress={continueWithGitHub}>
            {leaving ? <Spinner size="sm" color="current" aria-hidden="true" /> : <GitHubMark />}
            {leaving ? "Opening GitHub" : "Continue with GitHub"}
          </Button>
        </div>
        <p className="si-site-link">New to Talgent? <a href={PRODUCT_SITE}>Visit talgent.work</a></p>
      </div>
    </main>
  );
}
