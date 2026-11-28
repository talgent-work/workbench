import { Fragment } from "react";
import { Link } from "react-router-dom";

/** Where a person manages their Personal Variables. */
export function ManageVariablesLink({ className = "" }: { className?: string }) {
  return <Link to="/profile" className={`text-muted underline-offset-2 hover:underline ${className}`}>Manage variables in Profile</Link>;
}

/** Variable names in a sentence, comma-separated and set in monospace. A name
 * moves to the next line whole; one wider than the line breaks after an
 * underscore, not mid-word. */
export function VariableNames({ names }: { names: string[] }) {
  return names.map((name, index) => <Fragment key={name}>{index > 0 && " "}<span className="inline-block max-w-full"><code>{breakable(name)}</code>{index < names.length - 1 && ","}</span></Fragment>);
}

const breakable = (name: string) => name.split(/(?<=_)/).map((part, index) => <Fragment key={index}>{index > 0 && <wbr />}{part}</Fragment>);
