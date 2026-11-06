// The exit codes of talgent-computer. A run that ends with one of
// RUN_ENDED_FOR_GOOD stops for a reason only the person can resolve, so the
// background service does not start it again.
export const EXIT_OK = 0
export const EXIT_ERROR = 1
export const EXIT_USAGE = 2
// The server no longer accepts this Computer's credential: register again.
export const EXIT_CREDENTIAL_REJECTED = 3
// The Computer was removed from Talgent: register again to add it back.
export const EXIT_COMPUTER_REMOVED = 4
// run or status before register.
export const EXIT_NOT_REGISTERED = 5
// Nobody approved the registration in time.
export const EXIT_REGISTRATION_EXPIRED = 6
// Another run of this Computer's credential connected and took over.
export const EXIT_REPLACED = 7
// service status when no background service is installed.
export const EXIT_SERVICE_NOT_INSTALLED = 8
// The run ended to restart into the version installed now; whoever started
// it starts it again (src/upgrade/restart.ts).
export const EXIT_RESTART = 9

export const RUN_ENDED_FOR_GOOD: readonly number[] = [EXIT_CREDENTIAL_REJECTED, EXIT_COMPUTER_REMOVED, EXIT_NOT_REGISTERED, EXIT_REPLACED]

// Why a run ended, for the exit codes a person can act on.
export function exitReason(code: number): string | undefined {
  switch (code) {
    case EXIT_OK: return 'stopped'
    case EXIT_CREDENTIAL_REJECTED: return 'credential rejected; run "talgent-computer register --force"'
    case EXIT_COMPUTER_REMOVED: return 'Computer removed from Talgent; run "talgent-computer register --force"'
    case EXIT_NOT_REGISTERED: return 'not registered; run "talgent-computer register"'
    case EXIT_REPLACED: return 'another "talgent-computer run" took over'
    case EXIT_RESTART: return 'restarting into another version'
    default: return undefined
  }
}
