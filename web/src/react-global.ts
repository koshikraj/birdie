// The design system's bundle is a classic script that reads window.React, so
// this module must be imported before it.
import * as React from 'react';

declare global {
  interface Window {
    React: typeof React;
  }
}

window.React = React;
