// Typed access to the birdie design system's components. The bundle assigns
// window.Birdie as a side effect, using the app's own React.
import './react-global';
import '../../design/components/bundle.js';
import type { ComponentType } from 'react';
import type {
  AgentAvatarProps,
  AgentRowProps,
  BirdsProps,
  ButtonProps,
  InstallCommandProps,
  LetterProps,
  LogoProps,
  MessageBubbleProps,
  StatusSignalProps,
  TextFieldProps,
} from '../../design/components/index';

interface BirdieComponents {
  Logo: ComponentType<LogoProps>;
  Birds: ComponentType<BirdsProps>;
  Letter: ComponentType<LetterProps>;
  Button: ComponentType<ButtonProps>;
  InstallCommand: ComponentType<InstallCommandProps>;
  TextField: ComponentType<TextFieldProps>;
  AgentAvatar: ComponentType<AgentAvatarProps>;
  AgentRow: ComponentType<AgentRowProps>;
  MessageBubble: ComponentType<MessageBubbleProps>;
  StatusSignal: ComponentType<StatusSignalProps>;
}

const Birdie = (window as unknown as { Birdie: BirdieComponents }).Birdie;
if (!Birdie) throw new Error('the birdie design system did not load');

export const {
  Logo,
  Birds,
  Letter,
  Button,
  InstallCommand,
  TextField,
  AgentAvatar,
  AgentRow,
  MessageBubble,
  StatusSignal,
} = Birdie;

export type { ButtonProps, LetterProps, TextFieldProps };
