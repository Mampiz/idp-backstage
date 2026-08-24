export interface Config {
  /**
   * Configuration owned by this platform rather than by Backstage itself.
   */
  platform: {
    /**
     * Base URL of the Go scaffolder service that creates repositories and
     * applies WebApp custom resources.
     * @visibility backend
     */
    scaffolderBaseUrl: string;
  };
}
