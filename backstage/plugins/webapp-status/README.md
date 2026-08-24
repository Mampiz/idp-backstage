# webapp-status

Adds a **WebApp** tab to Backstage entity pages, showing the live state of the
`WebApp` custom resource the component runs as.

The tab is attached only to entities carrying the
`platform.miportfolio.com/webapp` annotation, whose value is `namespace/name`, so
components that are not deployed this way do not grow an empty tab.

Data comes from the Go status API through the Backstage proxy
(`proxy.endpoints['/webapp-status']`), polled every five seconds.
