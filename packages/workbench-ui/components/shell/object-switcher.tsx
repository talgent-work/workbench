import { Button, Dropdown, Header, Label, Separator, typographyVariants } from "@heroui/react";
import { Check, ChevronRight, ChevronsUpDown } from "lucide-react";
import type { ReactNode } from "react";
import "./object-switcher.css";

/** One object the switcher lists, such as a Project or an Application. */
export type SwitcherObject = { id: string; name: string; icon?: ReactNode };

/** A menu entry after the list, such as creating another object. */
export type SwitcherAction = { id: string; label: string; icon: ReactNode; onPress: () => void };

type Props = {
  /** What the objects are, as a singular noun: "Project", "Application". */
  noun: string;
  current: SwitcherObject;
  objects: SwitcherObject[];
  onSwitch: (id: string) => void;
  actions?: SwitcherAction[];
  /** A crumb follows the switcher, so its separator shows at rest. */
  followed?: boolean;
};

/** The first breadcrumb on an object's pages: its name, which opens the menu
 * that switches to another of the person's objects of that kind. One 16px slot
 * after the name holds the separator at rest and the switch chevron on hover
 * or keyboard focus (always on touch screens), so revealing the control moves
 * nothing. */
export function ObjectSwitcher({ noun, current, objects, onSwitch, actions = [], followed = true }: Props) {
  const plural = `${noun}s`;
  return (
    <Dropdown>
      <Button size="sm" variant="ghost" aria-label={`${noun} menu: ${current.name}`} className="p-object-switcher" data-followed={followed || undefined}>
        {current.icon}
        <span className={typographyVariants({ type: "body-sm", weight: "medium", truncate: true }).base()}>{current.name}</span>
        <span className="p-object-switcher-slot" aria-hidden="true">
          <ChevronRight size={14} className="p-object-switcher-separator" />
          <ChevronsUpDown size={14} className="p-object-switcher-chevron" />
        </span>
      </Button>
      <Dropdown.Popover placement="bottom start" className="p-sidebar-menu">
        <Dropdown.Menu aria-label={plural} onAction={key => {
          const action = actions.find(item => item.id === key);
          if (action) action.onPress(); else onSwitch(String(key));
        }}>
          <Dropdown.Section aria-label={`Switch ${noun.toLowerCase()}`}>
            <Header>Your {plural.toLowerCase()}</Header>
            {objects.map(item => (
              <Dropdown.Item key={item.id} id={item.id} textValue={item.name}>
                {item.icon}<Label>{item.name}</Label>
                {item.id === current.id && <Check size={16} aria-label={`Current ${noun.toLowerCase()}`} />}
              </Dropdown.Item>
            ))}
          </Dropdown.Section>
          {actions.length > 0 && <Separator />}
          {actions.map(action => <Dropdown.Item key={action.id} id={action.id} textValue={action.label}>{action.icon}<Label>{action.label}</Label></Dropdown.Item>)}
        </Dropdown.Menu>
      </Dropdown.Popover>
    </Dropdown>
  );
}
