CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    email VARCHAR(100) UNIQUE NOT NULL
);

-- Seed data for testing
INSERT INTO users (name, email) VALUES 
    ('Alice Smith', 'alice@example.com'),
    ('Bob Jones', 'bob@example.com')
ON CONFLICT DO NOTHING;
