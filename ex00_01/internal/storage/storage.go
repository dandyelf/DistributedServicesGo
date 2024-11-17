package storage

import (
	"fmt"
	"sync"
)

type Storage interface {
	Set(string, string) error
	Get(string) (string, error)
	Delete(string) error
	Save() (map[string]string, error)
	Load(map[string]string) error
}

type Cache struct {
	items map[string]string
	mu    sync.RWMutex
}

func NewCache() *Cache {
	items := make(map[string]string)
	return &Cache{items: items}
}

func (c *Cache) Set(id string, data string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[id] = data
	return nil
}

func (c *Cache) Get(id string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.items[id]; ok {
		return v, nil
	}
	return "", fmt.Errorf("can't get id: %v not found", id)
}

func (c *Cache) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[id]; ok {
		delete(c.items, id)
		return nil
	}
	return fmt.Errorf("can't delete id: %v not found", id)
}

func (c *Cache) Save() (map[string]string, error) {
	if len(c.items) == 0 {
		return nil, fmt.Errorf("cache is empty")
	}
	return c.items, nil
}

func (c *Cache) Load(items map[string]string) error {
	if len(items) == 0 {
		return fmt.Errorf("nothing to load")
	}
	c.items = items
	return nil
}

type JSONHelloResp struct {
	Addresses    []string
	ReplicFactor int
	Items        map[string]string
}
