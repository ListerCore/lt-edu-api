// Jest runs against a live lt-edu-api (API_URL, default http://localhost:5050) seeded with the demo institutions.
// Sessions come from cmd/demosessions (see setup/global-setup.js).
module.exports = {
  testEnvironment: 'node',
  testMatch: ['<rootDir>/specs/**/*.test.js'],
  globalSetup: '<rootDir>/setup/global-setup.js',
  globalTeardown: '<rootDir>/setup/global-teardown.js',
  testTimeout: 60_000,
  // Suites share the demo database: one at a time keeps them independent
  maxWorkers: 1,
  collectCoverageFrom: ['lib/**/*.js'],
  coverageDirectory: 'coverage/jest',
  coverageReporters: ['text', 'lcov', 'json-summary'],
  reporters: ['default', ['jest-junit', { outputDirectory: 'reports', outputName: 'junit.xml' }]],
};
