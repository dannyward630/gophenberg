Feature: A site waits for a database that starts late
  A server often starts Gophenberg before PostgreSQL answers, at boot or
  right after an update. serve then tries the database again, for up to
  GOPHENBERG_DATABASE_WAIT, 60 seconds by default, with a pause of
  GOPHENBERG_DATABASE_WAIT_INTERVAL, 1 second by default, between two
  tries. It logs once when it starts to wait and once when the database
  answers. Only a database that is not up yet is worth the wait: a
  connection refused, a socket that is not there yet, or a server that
  says it is still starting up. A wrong password, a missing role or a
  missing database stops serve at once, since waiting cannot fix them. A
  wait of 0 does not wait at all.

  Background:
    Given the settings point at an empty database
    And serve tries the database again every "100ms"

  Scenario: A site waits for a database that starts late
    Given the database refuses every connection for 2 seconds
    When the site starts and listens
    Then the command succeeds
    And the site logged once that it waits for the database
    And the site logged once that the database answered

  Scenario: A site stops once the database stays down past the wait
    Given the database refuses every connection
    And serve waits "1s" for the database
    When the site starts
    Then the command exits with code 1
    And the error says "the database did not answer within 1s, the time GOPHENBERG_DATABASE_WAIT allows"
    And the site logged once that it waits for the database

  Scenario: A site stops at once on a wrong password
    Given the settings name a wrong password for the database
    When the site starts
    Then the command exits with code 1
    And the error says "password authentication failed"
    And the site never waited for the database

  Scenario: A wait of 0 does not wait
    Given the database refuses every connection
    And serve waits "0" for the database
    When the site starts
    Then the command exits with code 1
    And the error says "the database did not answer within 0s, the time GOPHENBERG_DATABASE_WAIT allows"
    And the site never waited for the database
